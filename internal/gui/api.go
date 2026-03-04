package gui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/user/tls-client/pkg/fingerprint"
	"github.com/user/tls-client/pkg/transport"
)

// APIHandler serves the GUI API endpoints.
type APIHandler struct {
	state   *AppState
	engine  *EngineManager
	logger  *zap.Logger
	version string
	commit  string
	date    string
}

// NewAPIHandler creates the API handler.
func NewAPIHandler(state *AppState, engine *EngineManager, logger *zap.Logger, version, commit, date string) *APIHandler {
	return &APIHandler{
		state:   state,
		engine:  engine,
		logger:  logger,
		version: version,
		commit:  commit,
		date:    date,
	}
}

// RegisterRoutes registers all API routes on the given mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/start", h.handleStart)
	mux.HandleFunc("/api/stop", h.handleStop)
	mux.HandleFunc("/api/reload", h.handleReload)
	mux.HandleFunc("/api/config", h.handleConfig)
	mux.HandleFunc("/api/config/upload", h.handleConfigUpload)
	mux.HandleFunc("/api/fingerprints", h.handleFingerprints)
	mux.HandleFunc("/api/transports", h.handleTransports)
	mux.HandleFunc("/api/dial-metrics", h.handleDialMetrics)
	mux.HandleFunc("/api/logs", h.handleLogs)
	mux.HandleFunc("/api/logs/clear", h.handleLogsClear)
}

func (h *APIHandler) jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *APIHandler) errorResponse(w http.ResponseWriter, status int, msg string) {
	h.jsonResponse(w, status, map[string]string{"error": msg})
}

// --- /api/status ---

func (h *APIHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	status := h.state.GetEngineStatus()
	uptimeSeconds := 0
	if status == EngineStatusRunning {
		uptimeSeconds = int(time.Since(h.state.GetStartTime()).Seconds())
	}

	tunnelStats := h.engine.GetTunnelStats()
	var tunnelData map[string]int64
	if tunnelStats != nil {
		tunnelData = map[string]int64{
			"active_conns": tunnelStats.ActiveConns,
			"total_conns":  tunnelStats.TotalConns,
			"total_bytes":  tunnelStats.TotalBytes,
			"total_errors": tunnelStats.TotalErrors,
		}
	}

	h.jsonResponse(w, http.StatusOK, map[string]any{
		"engine_status":  status.String(),
		"version":        h.version,
		"commit":         h.commit,
		"built":          h.date,
		"uptime_seconds": uptimeSeconds,
		"goroutines":     runtime.NumGoroutine(),
		"memory": map[string]uint64{
			"alloc_mb": mem.Alloc / 1024 / 1024,
			"sys_mb":   mem.Sys / 1024 / 1024,
			"gc":       uint64(mem.NumGC),
		},
		"stats": map[string]int64{
			"total_conns":  h.state.GetTotalConns(),
			"active_conns": h.state.GetActiveConns(),
			"total_bytes":  h.state.GetTotalBytes(),
			"total_errors": h.state.GetTotalErrors(),
		},
		"tunnel":          tunnelData,
		"config_loaded":   h.state.GetConfigPath() != "" || h.state.GetConfigYAML() != "",
		"profiles_count":  fingerprint.Count(),
		"default_profile": fingerprint.DefaultProfile(),
		"last_error":      h.state.GetLastError(),
	})
}

// --- /api/start ---

func (h *APIHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	if err := h.engine.Start(); err != nil {
		h.errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.jsonResponse(w, http.StatusOK, map[string]string{
		"status":  "started",
		"message": "Engine started successfully",
	})
}

// --- /api/stop ---

func (h *APIHandler) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	if err := h.engine.Stop(); err != nil {
		h.errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.jsonResponse(w, http.StatusOK, map[string]string{
		"status":  "stopped",
		"message": "Engine stopped successfully",
	})
}

// --- /api/reload ---

func (h *APIHandler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// Check if request body contains YAML config
	var body struct {
		Config string `json:"config"`
	}
	if r.Body != nil {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	wasRunning := h.engine.IsRunning()
	if wasRunning {
		if err := h.engine.Stop(); err != nil {
			h.logger.Warn("stop before reload failed", zap.Error(err))
		}
	}

	if body.Config != "" {
		if err := h.engine.LoadConfigFromYAML(body.Config); err != nil {
			h.errorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid config: %v", err))
			return
		}
	} else if path := h.state.GetConfigPath(); path != "" {
		if err := h.engine.LoadConfig(path); err != nil {
			h.errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		h.errorResponse(w, http.StatusBadRequest, "no config available to reload")
		return
	}

	if wasRunning {
		if err := h.engine.Start(); err != nil {
			h.errorResponse(w, http.StatusInternalServerError, fmt.Sprintf("restart failed: %v", err))
			return
		}
	}

	h.jsonResponse(w, http.StatusOK, map[string]string{
		"status":  "reloaded",
		"message": "Config reloaded successfully",
	})
}

// --- /api/config ---

func (h *APIHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		yaml := h.state.GetConfigYAML()
		if yaml == "" {
			h.errorResponse(w, http.StatusNotFound, "no config loaded")
			return
		}
		h.jsonResponse(w, http.StatusOK, map[string]string{
			"config": yaml,
			"path":   h.state.GetConfigPath(),
		})

	case http.MethodPut:
		defer r.Body.Close()
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
		if err != nil {
			h.errorResponse(w, http.StatusBadRequest, "read body failed")
			return
		}
		var body struct {
			Config string `json:"config"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			h.errorResponse(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := h.engine.LoadConfigFromYAML(body.Config); err != nil {
			h.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		h.jsonResponse(w, http.StatusOK, map[string]string{
			"status": "config updated",
		})

	default:
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET or PUT only")
	}
}

// --- /api/config/upload ---

func (h *APIHandler) handleConfigUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.errorResponse(w, http.StatusBadRequest, "read failed")
		return
	}

	if err := h.engine.LoadConfigFromYAML(string(data)); err != nil {
		h.errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	h.jsonResponse(w, http.StatusOK, map[string]string{
		"status": "config uploaded and parsed",
	})
}

// --- /api/fingerprints ---

func (h *APIHandler) handleFingerprints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	profiles := make([]map[string]any, 0)
	for _, name := range fingerprint.List() {
		p := fingerprint.Get(name)
		profiles = append(profiles, map[string]any{
			"name":           p.Name,
			"browser":        p.Browser,
			"platform":       p.Platform,
			"version":        p.Version,
			"tags":           p.Tags,
			"user_agent":     p.UserAgent,
			"h2_fingerprint": p.H2Fingerprint(),
			"ja4h":           fingerprint.ComputeJA4H(p),
		})
	}

	h.jsonResponse(w, http.StatusOK, map[string]any{
		"profiles": profiles,
		"count":    len(profiles),
		"default":  fingerprint.DefaultProfile(),
	})
}

// --- /api/transports ---

func (h *APIHandler) handleTransports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	var transports []map[string]any
	for _, name := range transport.Names() {
		t := transport.Get(name)
		info := t.Info()
		transports = append(transports, map[string]any{
			"name":               name,
			"supports_multiplex": info.SupportsMultiplex,
			"supports_binary":    info.SupportsBinary,
			"requires_upgrade":   info.RequiresUpgrade,
			"max_frame_size":     info.MaxFrameSize,
			"alpn":               t.ALPNProtos(),
		})
	}

	h.jsonResponse(w, http.StatusOK, map[string]any{
		"transports": transports,
	})
}

// --- /api/dial-metrics ---

func (h *APIHandler) handleDialMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	metrics := h.engine.GetDialMetrics()
	avgMs := int64(0)
	if metrics.SuccessCount > 0 {
		avgMs = (metrics.TotalLatency / metrics.SuccessCount) / int64(time.Millisecond)
	}

	h.jsonResponse(w, http.StatusOK, map[string]any{
		"success_count":  metrics.SuccessCount,
		"failure_count":  metrics.FailureCount,
		"avg_latency_ms": avgMs,
	})
}

// --- /api/logs ---

func (h *APIHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.errorResponse(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	since := 0
	if s := r.URL.Query().Get("since"); s != "" {
		since, _ = strconv.Atoi(s)
	}

	logs := h.state.GetLogs(since)
	h.jsonResponse(w, http.StatusOK, map[string]any{
		"logs":  logs,
		"total": h.state.LogCount(),
	})
}

// --- /api/logs/clear ---

func (h *APIHandler) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.errorResponse(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	h.state.ClearLogs()
	h.state.AddLog("info", "Logs cleared")

	h.jsonResponse(w, http.StatusOK, map[string]string{
		"status": "logs cleared",
	})
}

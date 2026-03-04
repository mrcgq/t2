package gui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ServerConfig holds the configuration for the GUI server.
type ServerConfig struct {
	Addr       string
	Logger     *zap.Logger
	WebFS      embed.FS
	ConfigPath string
	Version    string
	Commit     string
	Date       string
}

// Server is the GUI HTTP server that embeds the Web UI and API.
type Server struct {
	cfg    ServerConfig
	state  *AppState
	engine *EngineManager
	api    *APIHandler
	http   *http.Server
}

// NewServer creates and configures the GUI server.
func NewServer(cfg ServerConfig) (*Server, error) {
	state := NewAppState()
	engine := NewEngineManager(state, cfg.Logger)
	api := NewAPIHandler(state, engine, cfg.Logger, cfg.Version, cfg.Commit, cfg.Date)

	// Load config if provided via command line
	if cfg.ConfigPath != "" {
		if err := engine.LoadConfig(cfg.ConfigPath); err != nil {
			cfg.Logger.Warn("failed to load initial config", zap.String("path", cfg.ConfigPath), zap.Error(err))
			state.AddLog("warn", fmt.Sprintf("Config load failed: %v", err))
		} else {
			state.AddLog("info", fmt.Sprintf("Config loaded from: %s", cfg.ConfigPath))
		}
	}

	return &Server{
		cfg:    cfg,
		state:  state,
		engine: engine,
		api:    api,
	}, nil
}

// Start begins serving the GUI.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// --- API routes ---
	s.api.RegisterRoutes(mux)

	// --- Health check ---
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// --- Static web UI ---
	// The embed.FS has path "web/index.html", we need to serve it at "/"
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only serve index.html for the root and non-API paths
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/api/") {
			// Try to serve as static file first
			data, err := fs.ReadFile(s.cfg.WebFS, "web"+r.URL.Path)
			if err == nil {
				contentType := "application/octet-stream"
				if strings.HasSuffix(r.URL.Path, ".html") {
					contentType = "text/html; charset=utf-8"
				} else if strings.HasSuffix(r.URL.Path, ".js") {
					contentType = "application/javascript"
				} else if strings.HasSuffix(r.URL.Path, ".css") {
					contentType = "text/css"
				} else if strings.HasSuffix(r.URL.Path, ".json") {
					contentType = "application/json"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write(data)
				return
			}
		}

		// Serve index.html for root and SPA fallback
		data, err := fs.ReadFile(s.cfg.WebFS, "web/index.html")
		if err != nil {
			s.cfg.Logger.Error("failed to read embedded index.html", zap.Error(err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write(data)
	})

	// --- CORS middleware for development ---
	handler := corsMiddleware(mux)

	s.http = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.state.AddLog("info", fmt.Sprintf("GUI server starting on %s", s.cfg.Addr))

	go func() {
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.cfg.Logger.Error("GUI server error", zap.Error(err))
			s.state.AddLog("error", fmt.Sprintf("Server error: %v", err))
		}
	}()

	return nil
}

// Stop gracefully shuts down the server and engine.
func (s *Server) Stop() {
	s.state.AddLog("info", "Shutting down...")

	// Stop engine first
	if s.engine.IsRunning() {
		if err := s.engine.Stop(); err != nil {
			s.cfg.Logger.Warn("engine stop error during shutdown", zap.Error(err))
		}
	}

	// Then stop HTTP server
	if s.http != nil {
		_ = s.http.Close()
	}
}

// corsMiddleware adds CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

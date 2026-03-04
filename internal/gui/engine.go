package gui

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/user/tls-client/pkg/config"
	"github.com/user/tls-client/pkg/engine"
	"github.com/user/tls-client/pkg/fingerprint"
	"github.com/user/tls-client/pkg/inbound"
	"github.com/user/tls-client/pkg/outbound"
	"github.com/user/tls-client/pkg/verify"
)

// EngineManager wraps the TLS-Client engine lifecycle.
type EngineManager struct {
	mu     sync.Mutex
	state  *AppState
	logger *zap.Logger

	cfg       *config.Config
	tunnel    *outbound.TunnelManager
	socks5    *inbound.SOCKS5Server
	httpProxy *inbound.HTTPProxyServer

	stopCh chan struct{}
}

// NewEngineManager creates an engine manager.
func NewEngineManager(state *AppState, logger *zap.Logger) *EngineManager {
	return &EngineManager{
		state:  state,
		logger: logger,
	}
}

// LoadConfig loads and validates a config file.
func (m *EngineManager) LoadConfig(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	m.cfg = cfg
	m.state.SetConfigPath(path)

	data, _ := os.ReadFile(path)
	m.state.SetConfigYAML(string(data))
	m.state.AddLog("info", fmt.Sprintf("Config loaded: %s", path))

	return nil
}

// LoadConfigFromYAML loads config from raw YAML string.
func (m *EngineManager) LoadConfigFromYAML(yamlContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Write to temp file for the config loader
	tmpFile, err := os.CreateTemp("", "tls-client-gui-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(yamlContent); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	tmpFile.Close()

	// Set secure permissions
	if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}

	cfg, err := config.Load(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	m.cfg = cfg
	m.state.SetConfigYAML(yamlContent)
	m.state.AddLog("info", "Config loaded from GUI")

	return nil
}

// Start launches the proxy engine.
func (m *EngineManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state.GetEngineStatus() == EngineStatusRunning {
		return fmt.Errorf("engine already running")
	}
	if m.cfg == nil {
		return fmt.Errorf("no config loaded")
	}

	m.state.SetEngineStatus(EngineStatusStarting)
	m.state.AddLog("info", "Engine starting...")

	// --- Build engine components ---

	vmode, err := verify.ParseMode(m.cfg.TLS.VerifyMode)
	if err != nil {
		m.state.SetEngineStatus(EngineStatusError)
		m.state.SetLastError(err.Error())
		return err
	}

	profileNames := m.cfg.Fingerprint.Rotation.Profiles
	if len(profileNames) == 0 && m.cfg.Fingerprint.Rotation.Profile != "" {
		profileNames = []string{m.cfg.Fingerprint.Rotation.Profile}
	}
	selector, err := fingerprint.NewSelector(m.cfg.Fingerprint.Rotation.Mode, profileNames)
	if err != nil {
		m.state.SetEngineStatus(EngineStatusError)
		m.state.SetLastError(err.Error())
		return err
	}

	activeNodeCfg := m.cfg.ActiveNode()
	if activeNodeCfg == nil {
		err := fmt.Errorf("no active node configured")
		m.state.SetEngineStatus(EngineStatusError)
		m.state.SetLastError(err.Error())
		return err
	}

	nodeProfile := selector.Select("")
	if activeNodeCfg.Fingerprint != "" {
		p := fingerprint.Get(activeNodeCfg.Fingerprint)
		if p != nil {
			nodeProfile = p
		}
	}

	node := outbound.NewNodeConfig(activeNodeCfg, nodeProfile, vmode, m.logger)
	m.tunnel = outbound.NewTunnelManager(node, m.logger)

	m.state.AddLog("info", fmt.Sprintf("Node: %s (%s) transport=%s profile=%s",
		node.Name, node.Address, node.Transport.Name(), nodeProfile.Name))

	// Wrap tunnel handler to track stats
	onConnect := func(clientConn net.Conn, target, domain string) {
		m.state.IncrTotalConns()
		m.state.IncrActiveConns()
		defer m.state.DecrActiveConns()
		m.tunnel.HandleConnect(clientConn, target, domain)
	}

	// --- Start inbound servers ---

	if m.cfg.Inbound.SOCKS5.Listen != "" {
		m.socks5 = inbound.NewSOCKS5Server(m.cfg.Inbound.SOCKS5.Listen, m.logger, onConnect)
		if err := m.socks5.Start(); err != nil {
			m.state.SetEngineStatus(EngineStatusError)
			m.state.SetLastError(err.Error())
			return fmt.Errorf("socks5 start: %w", err)
		}
		m.state.AddLog("info", fmt.Sprintf("SOCKS5 listening: %s", m.cfg.Inbound.SOCKS5.Listen))
	}

	if m.cfg.Inbound.HTTP.Listen != "" {
		m.httpProxy = inbound.NewHTTPProxyServer(m.cfg.Inbound.HTTP.Listen, m.logger, onConnect)
		if err := m.httpProxy.Start(); err != nil {
			// Clean up SOCKS5 if HTTP fails
			if m.socks5 != nil {
				m.socks5.Stop()
			}
			m.state.SetEngineStatus(EngineStatusError)
			m.state.SetLastError(err.Error())
			return fmt.Errorf("http proxy start: %w", err)
		}
		m.state.AddLog("info", fmt.Sprintf("HTTP proxy listening: %s", m.cfg.Inbound.HTTP.Listen))
	}

	m.stopCh = make(chan struct{})
	m.state.SetEngineStatus(EngineStatusRunning)
	m.state.SetStartTime(time.Now())
	m.state.SetLastError("")
	m.state.AddLog("info", "Engine started successfully")

	return nil
}

// Stop shuts down the engine.
func (m *EngineManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := m.state.GetEngineStatus()
	if status != EngineStatusRunning && status != EngineStatusError {
		return fmt.Errorf("engine not running (status=%s)", status.String())
	}

	m.state.SetEngineStatus(EngineStatusStopping)
	m.state.AddLog("info", "Engine stopping...")

	if m.socks5 != nil {
		m.socks5.Stop()
		m.socks5 = nil
	}
	if m.httpProxy != nil {
		m.httpProxy.Stop()
		m.httpProxy = nil
	}
	if m.tunnel != nil {
		m.tunnel.Close()
		m.tunnel = nil
	}
	if m.stopCh != nil {
		close(m.stopCh)
		m.stopCh = nil
	}

	m.state.SetEngineStatus(EngineStatusStopped)
	m.state.AddLog("info", "Engine stopped")

	return nil
}

// IsRunning returns whether the engine is running.
func (m *EngineManager) IsRunning() bool {
	return m.state.GetEngineStatus() == EngineStatusRunning
}

// GetTunnelStats returns tunnel stats if available.
func (m *EngineManager) GetTunnelStats() *outbound.TunnelStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tunnel == nil {
		return nil
	}
	stats := m.tunnel.Stats()
	return &stats
}

// GetDialMetrics returns dial metrics.
func (m *EngineManager) GetDialMetrics() engine.DialMetrics {
	return engine.GetDialMetrics()
}

// GetCurrentConfig returns the active config, if loaded.
func (m *EngineManager) GetCurrentConfig() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

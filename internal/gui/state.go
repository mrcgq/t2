package gui

import (
	"sync"
	"sync/atomic"
	"time"
)

// EngineStatus represents the current engine state.
type EngineStatus int32

const (
	EngineStatusStopped  EngineStatus = 0
	EngineStatusStarting EngineStatus = 1
	EngineStatusRunning  EngineStatus = 2
	EngineStatusStopping EngineStatus = 3
	EngineStatusError    EngineStatus = 4
)

func (s EngineStatus) String() string {
	switch s {
	case EngineStatusStopped:
		return "stopped"
	case EngineStatusStarting:
		return "starting"
	case EngineStatusRunning:
		return "running"
	case EngineStatusStopping:
		return "stopping"
	case EngineStatusError:
		return "error"
	default:
		return "unknown"
	}
}

// AppState holds all GUI application state, thread-safe.
type AppState struct {
	mu sync.RWMutex

	// Engine
	engineStatus int32 // atomic EngineStatus
	lastError    string
	startTime    time.Time

	// Config
	configPath string
	configYAML string // raw YAML content

	// Stats
	totalConns  int64
	activeConns int64
	totalBytes  int64
	totalErrors int64

	// Logs
	logsMu   sync.Mutex
	logLines []LogLine
	maxLogs  int
}

// LogLine is a single log entry.
type LogLine struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// NewAppState creates a new application state.
func NewAppState() *AppState {
	return &AppState{
		maxLogs: 2000,
	}
}

// --- Engine Status ---

func (s *AppState) SetEngineStatus(status EngineStatus) {
	atomic.StoreInt32(&s.engineStatus, int32(status))
}

func (s *AppState) GetEngineStatus() EngineStatus {
	return EngineStatus(atomic.LoadInt32(&s.engineStatus))
}

func (s *AppState) SetLastError(err string) {
	s.mu.Lock()
	s.lastError = err
	s.mu.Unlock()
}

func (s *AppState) GetLastError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastError
}

func (s *AppState) SetStartTime(t time.Time) {
	s.mu.Lock()
	s.startTime = t
	s.mu.Unlock()
}

func (s *AppState) GetStartTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startTime
}

// --- Config ---

func (s *AppState) SetConfigPath(path string) {
	s.mu.Lock()
	s.configPath = path
	s.mu.Unlock()
}

func (s *AppState) GetConfigPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configPath
}

func (s *AppState) SetConfigYAML(yaml string) {
	s.mu.Lock()
	s.configYAML = yaml
	s.mu.Unlock()
}

func (s *AppState) GetConfigYAML() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configYAML
}

// --- Stats ---

func (s *AppState) IncrTotalConns()       { atomic.AddInt64(&s.totalConns, 1) }
func (s *AppState) IncrActiveConns()      { atomic.AddInt64(&s.activeConns, 1) }
func (s *AppState) DecrActiveConns()      { atomic.AddInt64(&s.activeConns, -1) }
func (s *AppState) AddBytes(n int64)      { atomic.AddInt64(&s.totalBytes, n) }
func (s *AppState) IncrErrors()           { atomic.AddInt64(&s.totalErrors, 1) }
func (s *AppState) GetTotalConns() int64  { return atomic.LoadInt64(&s.totalConns) }
func (s *AppState) GetActiveConns() int64 { return atomic.LoadInt64(&s.activeConns) }
func (s *AppState) GetTotalBytes() int64  { return atomic.LoadInt64(&s.totalBytes) }
func (s *AppState) GetTotalErrors() int64 { return atomic.LoadInt64(&s.totalErrors) }

// --- Logs ---

func (s *AppState) AddLog(level, message string) {
	line := LogLine{
		Time:    time.Now().Format("15:04:05"),
		Level:   level,
		Message: message,
	}
	s.logsMu.Lock()
	s.logLines = append(s.logLines, line)
	if len(s.logLines) > s.maxLogs {
		s.logLines = s.logLines[len(s.logLines)-s.maxLogs:]
	}
	s.logsMu.Unlock()
}

func (s *AppState) GetLogs(since int) []LogLine {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	if since >= len(s.logLines) {
		return nil
	}
	result := make([]LogLine, len(s.logLines)-since)
	copy(result, s.logLines[since:])
	return result
}

func (s *AppState) ClearLogs() {
	s.logsMu.Lock()
	s.logLines = s.logLines[:0]
	s.logsMu.Unlock()
}

func (s *AppState) LogCount() int {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	return len(s.logLines)
}

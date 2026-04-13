package channel

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Status represents the lifecycle state of a channel
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusError    Status = "error"
	StatusStopping Status = "stopping"
)

// Channel is the interface every platform (Telegram, Discord, Slack, ...) must implement
type Channel interface {
	// Identity
	ID() string
	Type() string // "telegram", "discord", "slack", ...
	Name() string

	// Lifecycle
	Start(ctx context.Context) error
	Stop() error

	// Health
	Ping() error // lightweight check: can we reach the platform?
}

// ChannelConfig holds platform-agnostic configuration loaded from YAML
type ChannelConfig struct {
	ID           string            `yaml:"id"`
	Type         string            `yaml:"type"`           // telegram, discord, slack
	Name         string            `yaml:"name"`           // human-readable label
	Enabled      bool              `yaml:"enabled"`
	Token        string            `yaml:"token"`          // bot token
	Provider     string            `yaml:"provider"`       // "cli" or "api"
	Model        string            `yaml:"model"`          // claude model override
	SkillsDir    string            `yaml:"skills_dir"`     // per-channel skills
	ClaudeBinary string            `yaml:"claude_binary"`  // path to claude CLI
	MaxTurns     int               `yaml:"max_turns"`      // max agentic turns
	Options      map[string]string `yaml:"options"`
}

// Info is a snapshot of a running channel's state (exposed via admin API)
type Info struct {
	ID            string        `json:"id"`
	Type          string        `json:"type"`
	Name          string        `json:"name"`
	Status        Status        `json:"status"`
	Uptime        string        `json:"uptime"`
	UptimeSec     float64       `json:"uptime_seconds"`
	RestartCount  int           `json:"restart_count"`
	LastError     string        `json:"last_error,omitempty"`
	LastErrorTime *time.Time    `json:"last_error_time,omitempty"`
	LastPing      *time.Time    `json:"last_ping,omitempty"`
	PingLatency   string        `json:"ping_latency,omitempty"`
	SessionCount  int           `json:"session_count"`
	MessageCount  int64         `json:"message_count"`
}

// Metrics tracks per-channel runtime metrics (thread-safe)
type Metrics struct {
	mu            sync.RWMutex
	Status        Status
	StartedAt     time.Time
	RestartCount  int
	LastError     string
	LastErrorTime *time.Time
	LastPing      *time.Time
	PingLatency   time.Duration
	SessionCount  int
	MessageCount  int64
}

func NewMetrics() *Metrics {
	return &Metrics{Status: StatusStopped}
}

func (m *Metrics) SetStatus(s Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Status = s
	if s == StatusRunning {
		m.StartedAt = time.Now()
	}
}

func (m *Metrics) RecordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastError = err.Error()
	now := time.Now()
	m.LastErrorTime = &now
	m.Status = StatusError
}

func (m *Metrics) RecordPing(latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.LastPing = &now
	m.PingLatency = latency
}

func (m *Metrics) RecordRestart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RestartCount++
	m.StartedAt = time.Now()
}

func (m *Metrics) IncrMessages() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MessageCount++
}

func (m *Metrics) SetSessions(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SessionCount = n
}

func (m *Metrics) Snapshot(id, typ, name string) Info {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var uptime string
	var uptimeSec float64
	if m.Status == StatusRunning && !m.StartedAt.IsZero() {
		d := time.Since(m.StartedAt)
		uptimeSec = d.Seconds()
		uptime = formatDuration(d)
	}

	var pingLatency string
	if m.PingLatency > 0 {
		pingLatency = m.PingLatency.String()
	}

	return Info{
		ID:            id,
		Type:          typ,
		Name:          name,
		Status:        m.Status,
		Uptime:        uptime,
		UptimeSec:     uptimeSec,
		RestartCount:  m.RestartCount,
		LastError:     m.LastError,
		LastErrorTime: m.LastErrorTime,
		LastPing:      m.LastPing,
		PingLatency:   pingLatency,
		SessionCount:  m.SessionCount,
		MessageCount:  m.MessageCount,
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

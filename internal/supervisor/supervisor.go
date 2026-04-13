package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ez2k/claude-channel-hub/internal/bot"
	"github.com/ez2k/claude-channel-hub/internal/version"
)

// Config for supervisor behavior
type Config struct {
	HealthCheckInterval time.Duration
	MaxRestarts         int
	RestartDelay        time.Duration
	RestartBackoffMax   time.Duration
}

func DefaultConfig() Config {
	return Config{
		HealthCheckInterval: 30 * time.Second,
		MaxRestarts:         10,
		RestartDelay:        2 * time.Second,
		RestartBackoffMax:   5 * time.Minute,
	}
}

type botEntry struct {
	bot          *bot.Bot
	restartCount int
	lastRestart  time.Time
	restartCh    chan struct{} // manual restart signal
}

// Supervisor manages bot processes with health monitoring and auto-restart.
type Supervisor struct {
	config     Config
	versionMgr *version.Manager
	entries    []*botEntry
	mu         sync.RWMutex
	cancel     context.CancelFunc

	// Event log (ring buffer of last 1000 events)
	events   []Event
	eventsMu sync.Mutex
}

// Event records a supervisor action.
type Event struct {
	Time   time.Time `json:"time"`
	BotID  string    `json:"bot_id"`
	Action string    `json:"action"` // started, stopped, restarted, error, health_ok, health_fail
	Detail string    `json:"detail,omitempty"`
}

func New(cfg Config, versionMgr *version.Manager) *Supervisor {
	return &Supervisor{
		config:     cfg,
		versionMgr: versionMgr,
		events:     make([]Event, 0, 1000),
	}
}

// Register adds a bot to be supervised (does not start it).
func (s *Supervisor) Register(b *bot.Bot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, &botEntry{bot: b, restartCh: make(chan struct{}, 1)})
	s.logEvent(b.Config.ID, "registered", "")
}

// Start begins supervising all registered bots and runs the health check loop.
// It blocks until ctx is cancelled.
func (s *Supervisor) Start(ctx context.Context) {
	childCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.mu.RLock()
	count := 0
	for _, e := range s.entries {
		if e.bot.Config.Enabled {
			count++
			go s.runBot(childCtx, e)
		}
	}
	s.mu.RUnlock()

	log.Printf("🎛️  Supervisor started — managing %d bots", count)

	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-childCtx.Done():
			return
		case <-ticker.C:
			s.healthCheck()
		}
	}
}

// Stop gracefully stops all bot processes.
func (s *Supervisor) Stop() {
	log.Println("🛑 Supervisor stopping all bots...")
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.bot.Process != nil {
			if err := e.bot.Process.Stop(); err != nil {
				log.Printf("⚠️  [%s] Error stopping: %v", e.bot.Config.ID, err)
			}
			s.logEvent(e.bot.Config.ID, "stopped", "supervisor shutdown")
		}
	}
}

func (s *Supervisor) runBot(ctx context.Context, entry *botEntry) {
	delay := s.config.RestartDelay

	for attempt := 1; attempt <= s.config.MaxRestarts+1; attempt++ {
		if ctx.Err() != nil {
			return
		}

		log.Printf("🚀 [%s] Starting bot (attempt %d)...", entry.bot.Config.ID, attempt)
		s.logEvent(entry.bot.Config.ID, "started", fmt.Sprintf("attempt %d", attempt))

		claudeBin := s.versionMgr.Resolve(entry.bot.Config.ClaudeVersion)
		entry.bot.Process = bot.NewProcess(entry.bot, claudeBin)
		if err := entry.bot.Process.Start(ctx); err != nil {
			log.Printf("❌ [%s] Failed to start: %v", entry.bot.Config.ID, err)
			s.logEvent(entry.bot.Config.ID, "error", err.Error())
		} else {
			log.Printf("✅ [%s] Bot process started (pid %d)", entry.bot.Config.ID, entry.bot.Process.PID())
			entry.bot.Process.Wait()
		}

		if ctx.Err() != nil {
			return // shutdown requested
		}

		// Process exited unexpectedly
		if attempt > s.config.MaxRestarts {
			break
		}

		entry.restartCount++
		entry.lastRestart = time.Now()
		log.Printf("⚠️  [%s] Process exited, restarting in %s (restart %d/%d)",
			entry.bot.Config.ID, delay, entry.restartCount, s.config.MaxRestarts)
		s.logEvent(entry.bot.Config.ID, "restarted", fmt.Sprintf("restart #%d", entry.restartCount))

		select {
		case <-time.After(delay):
		case <-entry.restartCh:
			// Manual restart — reset backoff
			delay = s.config.RestartDelay
			entry.restartCount = 0
			attempt = 0 // will be incremented by loop
			log.Printf("🔄 [%s] Manual restart triggered", entry.bot.Config.ID)
			s.logEvent(entry.bot.Config.ID, "restarted", "manual restart — backoff reset")
		case <-ctx.Done():
			return
		}

		// Exponential backoff (capped)
		delay *= 2
		if delay > s.config.RestartBackoffMax {
			delay = s.config.RestartBackoffMax
		}
	}

	log.Printf("🚫 [%s] Max restarts (%d) exceeded — giving up", entry.bot.Config.ID, s.config.MaxRestarts)
	s.logEvent(entry.bot.Config.ID, "abandoned", fmt.Sprintf("max restarts %d reached", s.config.MaxRestarts))
}

func (s *Supervisor) healthCheck() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if !e.bot.Config.Enabled || e.bot.Process == nil {
			continue
		}
		if !e.bot.Process.IsAlive() && e.bot.Process.State() == bot.StateRunning {
			log.Printf("💔 [%s] Process not alive but state=running, will be restarted by runBot", e.bot.Config.ID)
			s.logEvent(e.bot.Config.ID, "health_fail", "process not alive")
		}
	}
}

// Status returns status of all bots.
func (s *Supervisor) Status() []BotStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []BotStatus
	for _, e := range s.entries {
		st := BotStatus{
			ID:           e.bot.Config.ID,
			Name:         e.bot.Config.Name,
			Type:         e.bot.Config.Type,
			Enabled:      e.bot.Config.Enabled,
			RestartCount: e.restartCount,
			ChannelCount: len(e.bot.Channels),
		}
		if e.bot.Process != nil {
			st.State = string(e.bot.Process.State())
			st.Uptime = time.Since(e.bot.Process.StartedAt()).String()
		} else {
			st.State = "idle"
		}
		result = append(result, st)
	}
	return result
}

// BotStatus is a snapshot of a bot's current state.
type BotStatus struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	State        string `json:"state"`
	Uptime       string `json:"uptime"`
	RestartCount int    `json:"restart_count"`
	ChannelCount int    `json:"channel_count"`
}

// ChannelInfo is a snapshot of a channel's configuration.
type ChannelInfo struct {
	ID           string `json:"id"`
	Bot          string `json:"bot"`
	Name         string `json:"name"`
	MatchType    string `json:"match_type"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	DataDir      string `json:"data_dir"`
}

// ChannelsForBot returns channels assigned to a specific bot.
func (s *Supervisor) ChannelsForBot(botID string) []ChannelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.bot.Config.ID == botID {
			return toChannelInfos(e.bot.Channels)
		}
	}
	return nil
}

// AllChannels returns all channels across all bots.
func (s *Supervisor) AllChannels() []ChannelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []ChannelInfo
	for _, e := range s.entries {
		result = append(result, toChannelInfos(e.bot.Channels)...)
	}
	return result
}

func toChannelInfos(channels []bot.ChannelConfig) []ChannelInfo {
	var result []ChannelInfo
	for _, ch := range channels {
		result = append(result, ChannelInfo{
			ID:           ch.ID,
			Bot:          ch.Bot,
			Name:         ch.Name,
			MatchType:    ch.Match.Type,
			Model:        ch.Model,
			SystemPrompt: ch.SystemPrompt,
			DataDir:      ch.DataDir,
		})
	}
	return result
}

// GetEvents returns recent supervisor events.
func (s *Supervisor) GetEvents(limit int) []Event {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}
	start := len(s.events) - limit
	if start < 0 {
		start = 0
	}
	result := make([]Event, limit)
	copy(result, s.events[start:])
	return result
}

// RestartBot signals the runBot goroutine for the given bot to restart immediately.
// It does NOT spawn a new goroutine — the existing runBot loop handles the restart.
func (s *Supervisor) RestartBot(botID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.bot.Config.ID == botID {
			if e.bot.Process != nil {
				e.bot.Process.Stop()
			}
			// Non-blocking send to signal runBot loop
			select {
			case e.restartCh <- struct{}{}:
			default:
			}
			s.logEvent(botID, "restart_requested", "manual restart via API")
			return nil
		}
	}
	return fmt.Errorf("bot %s not found", botID)
}

// ReadLog returns the last N lines from a bot's log file.
func (s *Supervisor) ReadLog(botID string, lines int) (string, error) {
	logPath := fmt.Sprintf("/tmp/claude-bot-%s.log", botID)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("read log: %w", err)
	}
	content := string(data)
	allLines := strings.Split(content, "\n")
	if len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return strings.Join(allLines, "\n"), nil
}

func (s *Supervisor) logEvent(botID, action, detail string) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	ev := Event{
		Time:   time.Now(),
		BotID:  botID,
		Action: action,
		Detail: detail,
	}
	s.events = append(s.events, ev)

	// Keep last 1000 events
	if len(s.events) > 1000 {
		s.events = s.events[len(s.events)-1000:]
	}
}

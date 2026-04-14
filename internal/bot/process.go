package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProcessState represents the lifecycle state of a bot process.
type ProcessState string

const (
	StateIdle     ProcessState = "idle"
	StateStarting ProcessState = "starting"
	StateRunning  ProcessState = "running"
	StateStopping ProcessState = "stopping"
	StateStopped  ProcessState = "stopped"
	StateFailed   ProcessState = "failed"
)

// Process manages a Claude Code bot running inside a tmux session.
type Process struct {
	bot          *Bot
	claudeBinary string
	tmuxSession  string // tmux session name
	state        ProcessState
	startedAt    time.Time
	mu           sync.RWMutex
	cancel       context.CancelFunc
}

// NewProcess creates a Process for the given bot (does not start it).
func NewProcess(b *Bot, claudeBinary string) *Process {
	if claudeBinary == "" {
		claudeBinary = "claude"
	}
	return &Process{
		bot:          b,
		claudeBinary: claudeBinary,
		tmuxSession:  "cch-" + b.Config.ID, // cch = claude-channel-hub
		state:        StateIdle,
	}
}

// State returns the current process state.
func (p *Process) State() ProcessState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// StartedAt returns the time the process was last started.
func (p *Process) StartedAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.startedAt
}

// Start launches Claude Code inside a tmux session.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == StateRunning {
		return fmt.Errorf("process already running")
	}
	p.state = StateStarting

	_, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	// Kill any existing tmux session with the same name
	exec.Command("tmux", "kill-session", "-t", p.tmuxSession).Run()

	// Kill stale bun processes using the same bot token (prevents 409 Conflict)
	killStaleBotProcesses(p.bot.Config.Token)

	// Build claude command args
	args := []string{"--dangerously-skip-permissions"}

	// Resume previous session if one exists
	home, _ := os.UserHomeDir()
	botDataDir := filepath.Join(home, ".claude-channel-hub", "data", p.bot.Config.ID)
	os.MkdirAll(botDataDir, 0755)

	sessDir := filepath.Join(botDataDir, ".claude", "sessions")
	if entries, err := os.ReadDir(sessDir); err == nil && len(entries) > 0 {
		args = append(args, "--continue")
	}

	// Channel mode
	if p.bot.Config.PluginMarketplace != "" {
		channelRef := fmt.Sprintf("plugin:%s@%s", p.bot.Config.Plugin, p.bot.Config.PluginMarketplace)
		args = append(args, "--channels", channelRef)
	} else if p.bot.Config.PluginDir != "" {
		pluginAbsDir, _ := filepath.Abs(p.bot.Config.PluginDir)
		srcMcp := filepath.Join(pluginAbsDir, ".mcp.json")
		dstMcp := filepath.Join(botDataDir, ".mcp.json")
		if data, err := os.ReadFile(srcMcp); err == nil {
			os.WriteFile(dstMcp, data, 0644)
		}
		serverName := fmt.Sprintf("server:%s", p.bot.Config.Plugin)
		args = append(args, "--dangerously-load-development-channels", serverName)
	}
	if p.bot.Config.Model != "" {
		args = append(args, "--model", p.bot.Config.Model)
	}
	if p.bot.Config.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", p.bot.Config.SystemPrompt)
	}

	// Build environment variables for tmux
	var envExports []string

	// Filter ANTHROPIC_API_KEY
	envExports = append(envExports, "unset ANTHROPIC_API_KEY")

	// Bot-specific env vars
	switch p.bot.Config.Type {
	case "telegram":
		envExports = append(envExports, fmt.Sprintf("export TELEGRAM_BOT_TOKEN='%s'", p.bot.Config.Token))
		stateDir := filepath.Join(home, ".claude", "channels", "telegram-"+p.bot.Config.ID)
		os.MkdirAll(stateDir, 0700)
		envExports = append(envExports, fmt.Sprintf("export TELEGRAM_STATE_DIR='%s'", stateDir))
		envFile := filepath.Join(stateDir, ".env")
		if _, err := os.Stat(envFile); os.IsNotExist(err) {
			os.WriteFile(envFile, []byte(fmt.Sprintf("TELEGRAM_BOT_TOKEN=%s\n", p.bot.Config.Token)), 0600)
		}
	case "discord":
		envExports = append(envExports, fmt.Sprintf("export DISCORD_BOT_TOKEN='%s'", p.bot.Config.Token))
	}

	channelsJSON, err := json.Marshal(p.bot.Channels)
	if err != nil {
		p.state = StateFailed
		cancel()
		return fmt.Errorf("marshal channels: %w", err)
	}
	envExports = append(envExports, fmt.Sprintf("export HARNESS_CHANNELS_CONFIG='%s'", string(channelsJSON)))
	envExports = append(envExports, fmt.Sprintf("export HARNESS_DATA_DIR='%s'", botDataDir))
	envExports = append(envExports, "export DISABLE_OMC=1")
	envExports = append(envExports, "export IS_SANDBOX=1")

	// Build the full command to run inside tmux
	claudeCmd := fmt.Sprintf("%s %s", p.claudeBinary, strings.Join(args, " "))
	// Chain: set env → cd to bot dir → run claude → log output
	logPath := fmt.Sprintf("/tmp/claude-bot-%s.log", p.bot.Config.ID)
	// Rotate log if > 10MB
	if info, err := os.Stat(logPath); err == nil && info.Size() > 10*1024*1024 {
		os.Rename(logPath, logPath+".1")
	}

	shellCmd := fmt.Sprintf("%s; cd '%s'; %s",
		strings.Join(envExports, "; "),
		botDataDir,
		claudeCmd,
	)

	// Create tmux session with the claude command
	tmuxArgs := []string{
		"new-session",
		"-d",                // detached
		"-s", p.tmuxSession, // session name
		"-x", "200",         // wide terminal
		"-y", "50",          // tall terminal
		"bash", "-c", shellCmd,
	}

	cmd := exec.Command("tmux", tmuxArgs...)
	if err := cmd.Run(); err != nil {
		p.state = StateFailed
		cancel()
		return fmt.Errorf("start tmux session: %w", err)
	}

	p.state = StateRunning
	p.startedAt = time.Now()

	// Log tmux output to file via pipe-pane
	exec.Command("tmux", "pipe-pane", "-t", p.tmuxSession, "-o", fmt.Sprintf("cat >> %s", logPath)).Run()

	// Auto-dismiss startup prompts after delays
	// Send Enter (CR) to confirm development channel warning
	go func() {
		for _, d := range []int{2, 4, 6, 8} {
			time.Sleep(time.Duration(d) * time.Second)
			exec.Command("tmux", "send-keys", "-t", p.tmuxSession, "Enter").Run()
		}
	}()

	// Monitor tmux session — detect when it exits
	go func() {
		for {
			time.Sleep(5 * time.Second)
			if !p.isTmuxAlive() {
				p.mu.Lock()
				if p.state == StateRunning {
					p.state = StateFailed
				}
				p.mu.Unlock()
				return
			}
		}
	}()

	return nil
}

// Stop gracefully stops the tmux session.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state != StateRunning {
		return nil
	}
	p.state = StateStopping

	// Send Ctrl-C to claude process, then kill session
	exec.Command("tmux", "send-keys", "-t", p.tmuxSession, "C-c", "").Run()
	time.Sleep(2 * time.Second)

	// Kill the tmux session
	exec.Command("tmux", "kill-session", "-t", p.tmuxSession).Run()

	if p.cancel != nil {
		p.cancel()
	}

	p.state = StateStopped
	return nil
}

// IsAlive reports whether the tmux session is running.
func (p *Process) IsAlive() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state == StateRunning && p.isTmuxAlive()
}

// isTmuxAlive checks if the tmux session exists (must be called without holding the lock, or from within a lock).
func (p *Process) isTmuxAlive() bool {
	err := exec.Command("tmux", "has-session", "-t", p.tmuxSession).Run()
	return err == nil
}

// PID returns the PID of the first process in the tmux session (0 if not running).
func (p *Process) PID() int {
	out, err := exec.Command("tmux", "list-panes", "-t", p.tmuxSession, "-F", "#{pane_pid}").Output()
	if err != nil {
		return 0
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

// Wait blocks until the tmux session exits.
func (p *Process) Wait() {
	for {
		time.Sleep(3 * time.Second)
		if !p.isTmuxAlive() {
			p.mu.Lock()
			if p.state != StateStopping {
				p.state = StateStopped
			}
			p.mu.Unlock()
			return
		}
	}
}

// SessionName returns the tmux session name for this bot.
func (p *Process) SessionName() string {
	return p.tmuxSession
}

// killStaleBotProcesses finds and kills bun/node processes that have the given
// bot token in their environment. This prevents Telegram 409 Conflict errors
// from multiple processes polling the same token.
func killStaleBotProcesses(token string) {
	if token == "" {
		return
	}
	// Short token prefix for matching (first 10 chars)
	prefix := token
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	// Find bun/node processes
	out, err := exec.Command("bash", "-c",
		fmt.Sprintf("ps aux | grep -E 'bun.*server|node.*claude' | grep -v grep | awk '{print $2}'")).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Check if this process has our token in its environment
		envData, err := os.ReadFile(fmt.Sprintf("/proc/%s/environ", line))
		if err != nil {
			continue
		}
		if strings.Contains(string(envData), prefix) {
			var pid int
			fmt.Sscanf(line, "%d", &pid)
			if pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					proc.Kill()
				}
			}
		}
	}
}

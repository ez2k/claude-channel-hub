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
	killStaleBotProcesses(p.bot.Config.Token, p.bot.Config.ID)

	// Build claude command args
	args := []string{"--dangerously-skip-permissions"}

	// Resume previous session if one exists
	home, _ := os.UserHomeDir()
	botDataDir := filepath.Join(home, ".claude-channel-hub", "data", p.bot.Config.ID)
	os.MkdirAll(botDataDir, 0755)

	// Check if bot has run before — use a marker file in bot data dir
	markerFile := filepath.Join(botDataDir, ".session-started")
	if _, err := os.Stat(markerFile); err == nil {
		// Previous session exists → resume it
		args = append(args, "--continue")
	}
	// Create marker after first launch
	os.WriteFile(markerFile, []byte(time.Now().Format(time.RFC3339)), 0644)

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
	// Load skills directory so Claude Code discovers SKILL.md files
	skillsAbsDir, _ := filepath.Abs("skills")
	if _, err := os.Stat(skillsAbsDir); err == nil {
		args = append(args, "--add-dir", skillsAbsDir)
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

	// Auto-answer prompts: background loop sends Enter to this tmux session after delays
	autoAnswer := fmt.Sprintf("(for d in 3 5 7 9; do sleep $d; tmux send-keys -t %s Enter 2>/dev/null; done) &", p.tmuxSession)
	shellCmd := fmt.Sprintf("%s; cd '%s'; %s %s",
		strings.Join(envExports, "; "),
		botDataDir,
		autoAnswer,
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

	// Auto-answer prompts is handled inside the shell command (autoAnswer)

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

// killStaleBotProcesses finds and kills bun/server.ts processes that could
// conflict with this bot's Telegram token. Checks both environment variables
// and the bot's state directory bot.pid file.
func killStaleBotProcesses(token string, botID string) {
	if token == "" {
		return
	}
	prefix := token
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}

	// 1. Kill any bun server.ts process with matching token in environment
	out, _ := exec.Command("bash", "-c",
		"ps aux | grep 'bun.*server' | grep -v grep | awk '{print $2}'").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		fmt.Sscanf(line, "%d", &pid)
		if pid <= 0 {
			continue
		}
		// Check environment for token
		envData, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if strings.Contains(string(envData), prefix) {
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
			continue
		}
		// Check if this is an official plugin process reading from same .env
		cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if strings.Contains(cwd, "telegram") {
			// Read the .env in that directory to see if it has our token
			envFile, _ := os.ReadFile(filepath.Join(cwd, ".env"))
			if strings.Contains(string(envFile), prefix) {
				if p, err := os.FindProcess(pid); err == nil {
					p.Kill()
				}
			}
		}
	}

	// 2. Kill stale process from bot.pid file
	home, _ := os.UserHomeDir()
	pidFile := filepath.Join(home, ".claude", "channels", "telegram-"+botID, "bot.pid")
	if data, err := os.ReadFile(pidFile); err == nil {
		var stalePid int
		fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &stalePid)
		if stalePid > 0 {
			if p, err := os.FindProcess(stalePid); err == nil {
				p.Kill()
			}
		}
	}
}

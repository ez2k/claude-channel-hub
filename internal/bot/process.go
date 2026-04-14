package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
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

// Process wraps an os/exec.Cmd running in a PTY and tracks lifecycle state.
type Process struct {
	bot          *Bot
	claudeBinary string
	cmd          *exec.Cmd
	ptmx         *os.File // PTY master
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
	return &Process{bot: b, claudeBinary: claudeBinary, state: StateIdle}
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

// Start launches `claude --channels ...` in a PTY so Claude Code sees a terminal.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == StateRunning {
		return fmt.Errorf("process already running")
	}
	p.state = StateStarting

	childCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	args := []string{"--dangerously-skip-permissions"}

	// Resume previous session if one exists in the bot's working directory
	h, _ := os.UserHomeDir()
	sessDir := filepath.Join(h, ".claude-channel-hub", "data", p.bot.Config.ID, ".claude", "sessions")
	if entries, err := os.ReadDir(sessDir); err == nil && len(entries) > 0 {
		args = append(args, "--continue")
	}

	// Channel mode: official plugin or development server
	if p.bot.Config.PluginMarketplace != "" {
		// Official plugin: plugin:name@marketplace via --channels
		channelRef := fmt.Sprintf("plugin:%s@%s", p.bot.Config.Plugin, p.bot.Config.PluginMarketplace)
		args = append(args, "--channels", channelRef)
	} else if p.bot.Config.PluginDir != "" {
		// Development channel: server:name via --dangerously-load-development-channels + --mcp-config
		pluginAbsDir, _ := filepath.Abs(p.bot.Config.PluginDir)
		mcpConfig := filepath.Join(pluginAbsDir, ".mcp.json")
		serverName := fmt.Sprintf("server:%s", p.bot.Config.Plugin)
		args = append(args, "--mcp-config", mcpConfig)
		args = append(args, "--dangerously-load-development-channels", serverName)
	}
	if p.bot.Config.Model != "" {
		args = append(args, "--model", p.bot.Config.Model)
	}
	if p.bot.Config.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", p.bot.Config.SystemPrompt)
	}

	cmd := exec.CommandContext(childCtx, p.claudeBinary, args...)

	// Build environment — filter out ANTHROPIC_API_KEY to prevent
	// Claude Code from prompting about API key usage (use subscription auth)
	var env []string
	for _, e := range os.Environ() {
		if len(e) > 18 && e[:18] == "ANTHROPIC_API_KEY=" {
			continue
		}
		env = append(env, e)
	}
	// Per-bot state directory for independent access control
	home, _ := os.UserHomeDir()
	switch p.bot.Config.Type {
	case "telegram":
		env = append(env, fmt.Sprintf("TELEGRAM_BOT_TOKEN=%s", p.bot.Config.Token))
		stateDir := filepath.Join(home, ".claude", "channels", "telegram-"+p.bot.Config.ID)
		os.MkdirAll(stateDir, 0700)
		env = append(env, fmt.Sprintf("TELEGRAM_STATE_DIR=%s", stateDir))
		// Copy token .env if not exists
		envFile := filepath.Join(stateDir, ".env")
		if _, err := os.Stat(envFile); os.IsNotExist(err) {
			os.WriteFile(envFile, []byte(fmt.Sprintf("TELEGRAM_BOT_TOKEN=%s\n", p.bot.Config.Token)), 0600)
		}
	case "discord":
		env = append(env, fmt.Sprintf("DISCORD_BOT_TOKEN=%s", p.bot.Config.Token))
	}

	channelsJSON, err := json.Marshal(p.bot.Channels)
	if err != nil {
		p.state = StateFailed
		cancel()
		return fmt.Errorf("marshal channels: %w", err)
	}
	env = append(env, fmt.Sprintf("HARNESS_CHANNELS_CONFIG=%s", string(channelsJSON)))

	// Per-bot data directory for memory/profile storage
	botDataDir := filepath.Join(home, ".claude-channel-hub", "data", p.bot.Config.ID)
	os.MkdirAll(botDataDir, 0755)
	env = append(env, fmt.Sprintf("HARNESS_DATA_DIR=%s", botDataDir))

	cmd.Env = env
	// Per-bot working directory so --continue resumes the correct session
	cmd.Dir = botDataDir
	p.cmd = cmd

	// Start in PTY — Claude Code requires a terminal to stay alive in interactive mode
	ptmx, err := pty.Start(cmd)
	if err != nil {
		p.state = StateFailed
		cancel()
		return fmt.Errorf("start claude with pty: %w", err)
	}
	p.ptmx = ptmx

	p.state = StateRunning
	p.startedAt = time.Now()

	// Capture PTY output to log file for debugging
	logPath := fmt.Sprintf("/tmp/claude-bot-%s.log", p.bot.Config.ID)
	// Rotate if > 10MB
	if info, err := os.Stat(logPath); err == nil && info.Size() > 10*1024*1024 {
		os.Rename(logPath, logPath+".1")
	}
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)

	// Auto-answer prompts: monitor PTY output for known prompts and send responses
	pr, pw := io.Pipe()
	go func() {
		buf := make([]byte, 4096)
		var recent []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// Write to log file
				if logFile != nil {
					logFile.Write(buf[:n])
				}
				// Write to pipe for downstream consumers
				pw.Write(buf[:n])
				// Check recent output for prompts
				recent = append(recent, buf[:n]...)
				if len(recent) > 8192 {
					recent = recent[len(recent)-8192:]
				}
				text := string(recent)
				// Auto-answer known prompts
				if containsPrompt(text, "Do you want to use this API key") {
					ptmx.Write([]byte("2\n")) // No
					recent = nil
				} else if containsPrompt(text, "Trust this workspace") ||
					containsPrompt(text, "trust this project") {
					ptmx.Write([]byte("y\n"))
					recent = nil
				} else if containsPrompt(text, "Yes") && containsPrompt(text, "No") &&
					containsPrompt(text, "1.") && containsPrompt(text, "2.") {
					ptmx.Write([]byte("1\n")) // Default to first option
					recent = nil
				}
			}
			if err != nil {
				pw.Close()
				if logFile != nil {
					logFile.Close()
				}
				return
			}
		}
	}()

	// Drain pipe to avoid blocking
	go func() {
		io.Copy(io.Discard, pr)
	}()

	// Transition state when process exits
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.ptmx != nil {
			p.ptmx.Close()
		}
		if err != nil && p.state == StateRunning {
			p.state = StateFailed
		} else if p.state != StateStopping {
			p.state = StateStopped
		}
	}()

	return nil
}

// Stop gracefully stops the claude process.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state != StateRunning {
		return nil
	}
	p.state = StateStopping

	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(os.Interrupt)

		done := make(chan struct{})
		go func() {
			if p.cmd.ProcessState == nil {
				_, _ = p.cmd.Process.Wait()
			}
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
		}
	}

	if p.ptmx != nil {
		p.ptmx.Close()
	}

	if p.cancel != nil {
		p.cancel()
	}

	p.state = StateStopped
	return nil
}

// IsAlive reports whether the process is currently running.
func (p *Process) IsAlive() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	return p.state == StateRunning
}

// PID returns the OS process ID (0 if not started).
func (p *Process) PID() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Wait blocks until the process exits.
func (p *Process) Wait() {
	if p.cmd == nil {
		return
	}
	_ = p.cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateStopping {
		p.state = StateStopped
	}
}

// Output returns the PTY master for reading process output.
func (p *Process) Output() *os.File { return p.ptmx }

// containsPrompt checks if text contains a prompt string (case-insensitive, ignoring ANSI codes).
func containsPrompt(text, prompt string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(prompt))
}

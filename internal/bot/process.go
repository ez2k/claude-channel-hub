package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

	// Build channel reference
	channelRef := p.bot.Config.Plugin
	if p.bot.Config.PluginMarketplace != "" {
		channelRef = fmt.Sprintf("plugin:%s@%s", p.bot.Config.Plugin, p.bot.Config.PluginMarketplace)
	} else {
		channelRef = fmt.Sprintf("server:%s", p.bot.Config.Plugin)
	}

	args := []string{
		"--channels", channelRef,
		"--dangerously-skip-permissions",
	}
	if p.bot.Config.PluginMarketplace == "" && p.bot.Config.PluginDir != "" {
		args = append(args, "--plugin-dir", p.bot.Config.PluginDir)
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
	switch p.bot.Config.Type {
	case "telegram":
		env = append(env, fmt.Sprintf("TELEGRAM_BOT_TOKEN=%s", p.bot.Config.Token))
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
	cmd.Env = env
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
	logFile, _ := os.Create(logPath)
	go func() {
		if logFile != nil {
			io.Copy(logFile, ptmx)
			logFile.Close()
		} else {
			io.Copy(io.Discard, ptmx)
		}
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

package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLIProvider calls the local `claude` binary as a subprocess.
// Uses whatever auth is configured in Claude Code (subscription OAuth or API key).
// No session is recorded because -p mode is stateless.
type CLIProvider struct {
	binaryPath   string
	model        string
	maxTurns     int
	timeout      time.Duration
}

// CLIConfig for creating a CLI provider
type CLIConfig struct {
	BinaryPath string // path to claude binary, default "claude"
	Model      string // model override, empty = use default
	MaxTurns   int    // max agentic turns, default 10
	Timeout    time.Duration
}

func NewCLIProvider(cfg CLIConfig) *CLIProvider {
	if cfg.BinaryPath == "" {
		cfg.BinaryPath = "claude"
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 10
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}

	return &CLIProvider{
		binaryPath: cfg.BinaryPath,
		model:      cfg.Model,
		maxTurns:   cfg.MaxTurns,
		timeout:    cfg.Timeout,
	}
}

func (c *CLIProvider) Mode() string { return "cli" }

func (c *CLIProvider) Send(systemPrompt string, messages []Message, tools []ToolDef) (*Response, error) {
	// Build the prompt from conversation history
	prompt := buildPromptFromMessages(messages)

	// Build claude -p command args
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--max-turns", fmt.Sprintf("%d", c.maxTurns),
	}

	// System prompt
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Model override
	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	// Execute claude CLI
	cmd := exec.Command(c.binaryPath, args...)

	// CLI mode uses subscription auth — remove ANTHROPIC_API_KEY to prevent
	// the CLI from attempting API key authentication with a potentially invalid key.
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	// Set timeout
	done := make(chan struct{})
	var output []byte
	var cmdErr error

	go func() {
		output, cmdErr = cmd.CombinedOutput()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(c.timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return nil, fmt.Errorf("claude CLI timed out after %s", c.timeout)
	}

	if cmdErr != nil {
		// Check if it's a non-zero exit with output (claude sometimes exits non-zero with valid output)
		if len(output) == 0 {
			return nil, fmt.Errorf("claude CLI error: %w (output: %s)", cmdErr, string(output))
		}
	}

	// Parse JSON response
	return parseCLIResponse(output)
}

// buildPromptFromMessages constructs a single prompt from message history
// For CLI mode, we concatenate the conversation into a single prompt
// since -p mode doesn't support multi-turn natively
func buildPromptFromMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	// If only one user message, use it directly
	if len(messages) == 1 {
		return messages[0].Content
	}

	// Multi-turn: build context block + latest message
	var sb strings.Builder
	sb.WriteString("<conversation_history>\n")

	for _, m := range messages[:len(messages)-1] {
		role := "Human"
		if m.Role == "assistant" {
			role = "Assistant"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, m.Content))
	}

	sb.WriteString("</conversation_history>\n\n")
	sb.WriteString("Continue the conversation. The latest message:\n\n")
	sb.WriteString(messages[len(messages)-1].Content)

	return sb.String()
}

// parseCLIResponse parses the JSON output from `claude -p --output-format json`
// The output is a JSON array: [{"type":"system",...}, {"type":"assistant",...}, {"type":"result",...}]
func parseCLIResponse(output []byte) (*Response, error) {
	outputStr := strings.TrimSpace(string(output))

	// Try parsing as JSON array (standard claude -p --output-format json output)
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(outputStr), &entries); err == nil {
		return parseCLIEntries(entries)
	}

	// Try parsing as single JSON object (fallback)
	var single map[string]interface{}
	if err := json.Unmarshal([]byte(outputStr), &single); err == nil {
		return parseCLISingleObject(single)
	}

	// If all JSON parsing fails, treat as plain text
	return &Response{Text: outputStr, Done: true}, nil
}

// parseCLIEntries processes the JSON array from claude CLI output
func parseCLIEntries(entries []json.RawMessage) (*Response, error) {
	resp := &Response{Done: true}
	var textParts []string

	for _, raw := range entries {
		var entry struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "assistant":
			for _, block := range entry.Message.Content {
				if block.Type == "text" && block.Text != "" {
					textParts = append(textParts, block.Text)
				}
			}
		case "result":
			if entry.Result != "" {
				// Use result as the final text (most reliable)
				resp.Text = entry.Result
				resp.Done = true
				if entry.IsError {
					return resp, fmt.Errorf("claude CLI error: %s", entry.Result)
				}
				return resp, nil
			}
		}
	}

	if resp.Text == "" && len(textParts) > 0 {
		resp.Text = strings.Join(textParts, "\n")
	}

	return resp, nil
}

// parseCLISingleObject handles a single JSON object response (legacy/fallback)
func parseCLISingleObject(obj map[string]interface{}) (*Response, error) {
	resp := &Response{Done: true}

	if result, ok := obj["result"].(string); ok && result != "" {
		resp.Text = result
		return resp, nil
	}

	if content, ok := obj["content"].([]interface{}); ok {
		var textParts []string
		for _, block := range content {
			if m, ok := block.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		resp.Text = strings.Join(textParts, "\n")
	}

	return resp, nil
}

// CheckInstalled verifies the claude CLI is available
func CheckCLIInstalled(binaryPath string) error {
	if binaryPath == "" {
		binaryPath = "claude"
	}

	cmd := exec.Command(binaryPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude CLI not found at '%s': %w\nInstall: npm install -g @anthropic-ai/claude-code", binaryPath, err)
	}

	fmt.Printf("🔗 Claude CLI: %s", strings.TrimSpace(string(output)))
	return nil
}

// CheckAuth verifies the claude CLI is authenticated
func CheckCLIAuth(binaryPath string) (string, error) {
	if binaryPath == "" {
		binaryPath = "claude"
	}

	cmd := exec.Command(binaryPath, "auth", "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude auth check failed: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

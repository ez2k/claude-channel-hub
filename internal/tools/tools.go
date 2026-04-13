package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Tool represents an executable tool the agent can use
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	Execute     func(input map[string]interface{}) (string, error) `json:"-"`
}

// Registry holds all available tools
type Registry struct {
	tools map[string]*Tool
}

func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]*Tool)}
	r.registerDefaults()
	return r
}

func (r *Registry) registerDefaults() {
	// 1. Bash command execution
	r.Register(&Tool{
		Name:        "bash",
		Description: "Execute a bash command. Use for file operations, running scripts, system commands. Commands run in a sandboxed environment.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The bash command to execute",
				},
			},
			"required": []string{"command"},
		},
		Execute: executeBash,
	})

	// 2. File read
	r.Register(&Tool{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
			},
			"required": []string{"path"},
		},
		Execute: executeReadFile,
	})

	// 3. File write
	r.Register(&Tool{
		Name:        "write_file",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to write the file to",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Content to write",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: executeWriteFile,
	})

	// 4. List directory
	r.Register(&Tool{
		Name:        "list_dir",
		Description: "List files and directories at the given path.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory path to list",
				},
			},
			"required": []string{"path"},
		},
		Execute: executeListDir,
	})

	// 5. Web fetch (simple)
	r.Register(&Tool{
		Name:        "web_fetch",
		Description: "Fetch the text content of a URL.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to fetch",
				},
			},
			"required": []string{"url"},
		},
		Execute: executeWebFetch,
	})
}

func (r *Registry) Register(t *Tool) {
	r.tools[t.Name] = t
}

func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []*Tool {
	result := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToAPIFormat converts tools to Anthropic API tool format
func (r *Registry) ToAPIFormat() []map[string]interface{} {
	var apiTools []map[string]interface{}
	for _, t := range r.tools {
		apiTools = append(apiTools, map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return apiTools
}

// --- Tool implementations ---

func executeBash(input map[string]interface{}) (string, error) {
	command, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	// Safety: block dangerous commands
	blocked := []string{"rm -rf /", "mkfs", "dd if=", ":(){", "fork bomb"}
	for _, b := range blocked {
		if strings.Contains(command, b) {
			return "", fmt.Errorf("blocked: dangerous command detected")
		}
	}

	ctx_timeout := 30 * time.Second
	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = os.TempDir()

	done := make(chan error, 1)
	var output []byte

	go func() {
		var err error
		output, err = cmd.CombinedOutput()
		done <- err
	}()

	select {
	case err := <-done:
		result := string(output)
		if len(result) > 10000 {
			result = result[:10000] + "\n... (truncated)"
		}
		if err != nil {
			return fmt.Sprintf("exit code: %v\n%s", err, result), nil
		}
		return result, nil
	case <-time.After(ctx_timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("command timed out after %s", ctx_timeout)
	}
}

func executeReadFile(input map[string]interface{}) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	if len(content) > 50000 {
		content = content[:50000] + "\n... (truncated)"
	}
	return content, nil
}

func executeWriteFile(input map[string]interface{}) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	content, ok := input["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
}

func executeListDir(input map[string]interface{}) (string, error) {
	path, ok := input["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("failed to list directory: %w", err)
	}

	var sb strings.Builder
	for _, e := range entries {
		prefix := "📄"
		if e.IsDir() {
			prefix = "📁"
		}
		info, _ := e.Info()
		size := ""
		if info != nil && !e.IsDir() {
			size = fmt.Sprintf(" (%d bytes)", info.Size())
		}
		sb.WriteString(fmt.Sprintf("%s %s%s\n", prefix, e.Name(), size))
	}
	return sb.String(), nil
}

func executeWebFetch(input map[string]interface{}) (string, error) {
	url, ok := input["url"].(string)
	if !ok {
		return "", fmt.Errorf("url must be a string")
	}

	cmd := exec.Command("curl", "-sL", "--max-time", "10", "-A", "ClaudeHarness/1.0", url)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}

	content := string(output)
	if len(content) > 30000 {
		content = content[:30000] + "\n... (truncated)"
	}
	return content, nil
}

// ParseInput converts raw JSON input to map
func ParseInput(raw json.RawMessage) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

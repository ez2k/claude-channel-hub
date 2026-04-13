package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadEnvFile reads a .env file and sets environment variables.
// Lines starting with # and empty lines are skipped.
// Existing environment variables are NOT overwritten.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Remove surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		// Don't overwrite existing env vars
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

// Root is the top-level config from channels.yaml
type Root struct {
	Admin      AdminConfig      `yaml:"admin"`
	Supervisor SupervisorConfig `yaml:"supervisor"`
	Claude     ClaudeConfig     `yaml:"claude"`
	Defaults   DefaultsConfig   `yaml:"defaults"`
	Bots       []BotConfig      `yaml:"bots"`
	Channels   []ChannelConfig  `yaml:"channels"`
}

type AdminConfig struct {
	Addr string `yaml:"addr"`
}

type SupervisorConfig struct {
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
	MaxRestarts         int           `yaml:"max_restarts"`
	RestartDelay        time.Duration `yaml:"restart_delay"`
	RestartBackoffMax   time.Duration `yaml:"restart_backoff_max"`
}

type ClaudeConfig struct {
	DefaultVersion     string        `yaml:"default_version"`
	VersionsDir        string        `yaml:"versions_dir"`
	AutoUpdate         bool          `yaml:"auto_update"`
	AutoUpdateInterval time.Duration `yaml:"auto_update_interval"`
}

type DefaultsConfig struct {
	Model          string `yaml:"model"`
	SystemPrompt   string `yaml:"system_prompt"`
	PermissionMode string `yaml:"permission_mode"`
	PluginsDir     string `yaml:"plugins_dir"`
}

type BotConfig struct {
	ID            string `yaml:"id"`
	Type          string `yaml:"type"`            // "telegram", "discord"
	Name          string `yaml:"name"`
	Enabled       bool   `yaml:"enabled"`
	Token         string `yaml:"token"`
	Plugin           string `yaml:"plugin"`              // "telegram"
	PluginDir        string `yaml:"plugin_dir"`          // path to plugin directory (local/dev)
	PluginMarketplace string `yaml:"plugin_marketplace"` // "claude-plugins-official"
	ClaudeVersion    string `yaml:"claude_version"`      // "" = default
	Model         string `yaml:"model"`           // "" = defaults.model
	SystemPrompt  string `yaml:"system_prompt"`   // appended to default
}

type ChannelConfig struct {
	ID           string      `yaml:"id"`
	Bot          string      `yaml:"bot"`           // references BotConfig.ID
	Name         string      `yaml:"name"`
	Match        MatchConfig `yaml:"match"`
	Model        string      `yaml:"model"`         // "" = bot's model
	SystemPrompt string      `yaml:"system_prompt"`
	DataDir      string      `yaml:"data_dir"`
}

type MatchConfig struct {
	Type     string   `yaml:"type"`      // "default", "group", "user", "topic"
	ChatIDs  []string `yaml:"chat_ids"`
	UserIDs  []string `yaml:"user_ids"`
	TopicIDs []string `yaml:"topic_ids"`
}

// Load reads the YAML config file and resolves env vars
func Load(path string) (*Root, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	// Resolve ${ENV_VAR} references
	content := resolveEnvVars(string(data))

	var root Root
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply defaults
	if root.Admin.Addr == "" {
		root.Admin.Addr = ":8080"
	}
	if root.Claude.DefaultVersion == "" {
		root.Claude.DefaultVersion = "latest"
	}
	if root.Defaults.PermissionMode == "" {
		root.Defaults.PermissionMode = "dangerously-skip"
	}
	if root.Defaults.PluginsDir == "" {
		root.Defaults.PluginsDir = "./plugins"
	}
	if root.Supervisor.HealthCheckInterval == 0 {
		root.Supervisor.HealthCheckInterval = 30 * time.Second
	}
	if root.Supervisor.MaxRestarts == 0 {
		root.Supervisor.MaxRestarts = 10
	}
	if root.Supervisor.RestartDelay == 0 {
		root.Supervisor.RestartDelay = 2 * time.Second
	}
	if root.Supervisor.RestartBackoffMax == 0 {
		root.Supervisor.RestartBackoffMax = 5 * time.Minute
	}

	// Apply defaults to bots
	for i := range root.Bots {
		if root.Bots[i].Model == "" {
			root.Bots[i].Model = root.Defaults.Model
		}
		if root.Bots[i].PluginDir == "" {
			root.Bots[i].PluginDir = root.Defaults.PluginsDir + "/" + root.Bots[i].Plugin
		}
	}

	// Apply defaults to channels
	for i := range root.Channels {
		if root.Channels[i].Match.Type == "" {
			root.Channels[i].Match.Type = "default"
		}
	}

	return &root, nil
}

// resolveEnvVars replaces ${VAR_NAME} with environment variable values
func resolveEnvVars(s string) string {
	result := s
	for {
		start := strings.Index(result, "${")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end == -1 {
			break
		}
		end += start

		varName := result[start+2 : end]
		varValue := os.Getenv(varName)
		result = result[:start] + varValue + result[end+1:]
	}
	return result
}

package bot

// Bot represents a platform bot (one token = one Claude Code process)
type Bot struct {
	Config   BotConfig
	Channels []ChannelConfig // channels assigned to this bot
	Process  *Process        // managed claude process (nil if not running)
}

// BotConfig matches config.BotConfig but is self-contained
type BotConfig struct {
	ID            string
	Type          string
	Name          string
	Enabled       bool
	Token         string
	Plugin           string
	PluginDir        string // path to plugin directory (for local/dev plugins)
	PluginMarketplace string // marketplace name (e.g., "claude-plugins-official")
	ClaudeVersion    string
	Model         string
	SystemPrompt  string // appended to default system prompt
}

// ChannelConfig holds per-channel routing configuration for a bot
type ChannelConfig struct {
	ID           string
	Bot          string
	Name         string
	Match        MatchConfig
	Model        string
	SystemPrompt string
	DataDir      string
}

// MatchConfig defines how incoming messages are matched to a channel
type MatchConfig struct {
	Type     string
	ChatIDs  []string
	UserIDs  []string
	TopicIDs []string
}

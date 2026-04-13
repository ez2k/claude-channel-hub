package provider

// Response from a provider call
type Response struct {
	Text     string     // final text output
	ToolUses []ToolCall // tool calls made
	Done     bool       // true if no more tool calls needed
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

// Provider abstracts how we communicate with Claude
type Provider interface {
	// Send sends a message and returns the response
	// systemPrompt: full system prompt including skills, memory, profile
	// messages: conversation history as simple role/content pairs
	// tools: tool definitions (only used by API provider)
	Send(systemPrompt string, messages []Message, tools []ToolDef) (*Response, error)

	// Mode returns "api" or "cli"
	Mode() string
}

// Message is a simple role/content pair for provider input
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// ToolDef is a simplified tool definition
type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

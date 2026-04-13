package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// APIProvider calls the Anthropic API directly using an API key.
type APIProvider struct {
	client    *anthropic.Client
	model     string
	maxTokens int
}

type APIConfig struct {
	APIKey    string
	Model     string
	MaxTokens int
}

func NewAPIProvider(cfg APIConfig) *APIProvider {
	client := anthropic.NewClient(option.WithAPIKey(cfg.APIKey))
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	return &APIProvider{
		client:    &client,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
	}
}

func (a *APIProvider) Mode() string { return "api" }

func (a *APIProvider) Send(systemPrompt string, messages []Message, tools []ToolDef) (*Response, error) {
	// Convert messages to Anthropic format
	var apiMessages []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case "user":
			apiMessages = append(apiMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			apiMessages = append(apiMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	// Convert tools
	var apiTools []anthropic.ToolUnionParam
	for _, t := range tools {
		schemaBytes, _ := json.Marshal(t.InputSchema)
		var schema anthropic.ToolInputSchemaParam
		json.Unmarshal(schemaBytes, &schema)
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: schema,
			},
		})
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: int64(a.maxTokens),
		Messages:  apiMessages,
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: systemPrompt}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}

	result, err := a.client.Messages.New(context.Background(), params)
	if err != nil {
		return nil, fmt.Errorf("API error: %w", err)
	}

	// Convert response
	resp := &Response{Done: result.StopReason == "end_turn"}

	var textParts []string
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			resp.ToolUses = append(resp.ToolUses, ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: string(block.Input),
			})
		}
	}
	resp.Text = strings.Join(textParts, "\n")

	return resp, nil
}

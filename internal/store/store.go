package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Message is a serializable chat message (decoupled from Anthropic SDK types)
type Message struct {
	Role      string    `json:"role"`       // "user" or "assistant"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	ToolUse   []ToolUse `json:"tool_use,omitempty"`
}

type ToolUse struct {
	Name   string `json:"name"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

// Conversation holds a full conversation with metadata
type Conversation struct {
	ID          string    `json:"id"`           // e.g. "12345" (chat ID)
	ChannelID   string    `json:"channel_id"`
	Title       string    `json:"title"`        // first user message (truncated)
	Messages    []Message `json:"messages"`
	ActiveSkill string    `json:"active_skill,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	MessageCount int      `json:"message_count"`
}

// ConversationSummary is a lightweight view for listing
type ConversationSummary struct {
	ID           string    `json:"id"`
	ChannelID    string    `json:"channel_id"`
	Title        string    `json:"title"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ActiveSkill  string    `json:"active_skill,omitempty"`
}

// Store defines the conversation persistence interface
type Store interface {
	// Save persists a conversation
	Save(conv *Conversation) error

	// Load retrieves a conversation by channel + conversation ID
	Load(channelID, convID string) (*Conversation, error)

	// Delete removes a conversation
	Delete(channelID, convID string) error

	// ListByChannel returns summaries for all conversations in a channel
	ListByChannel(channelID string) ([]ConversationSummary, error)

	// ListChannels returns all channel IDs that have stored conversations
	ListChannels() ([]string, error)

	// Stats returns storage stats per channel
	Stats(channelID string) (ChannelStats, error)
}

// ChannelStats provides aggregate info for a channel's conversations
type ChannelStats struct {
	ChannelID        string `json:"channel_id"`
	ConversationCount int   `json:"conversation_count"`
	TotalMessages    int    `json:"total_messages"`
	OldestConv       string `json:"oldest_conversation,omitempty"`
	NewestConv       string `json:"newest_conversation,omitempty"`
}

// --- File-based implementation ---

// FileStore persists conversations as JSON files on disk
// Layout: {baseDir}/{channelID}/{convID}.json
type FileStore struct {
	baseDir string
	mu      sync.RWMutex
}

func NewFileStore(baseDir string) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store dir: %w", err)
	}
	return &FileStore{baseDir: baseDir}, nil
}

func (fs *FileStore) channelDir(channelID string) string {
	return filepath.Join(fs.baseDir, sanitize(channelID))
}

func (fs *FileStore) convPath(channelID, convID string) string {
	return filepath.Join(fs.channelDir(channelID), sanitize(convID)+".json")
}

func (fs *FileStore) Save(conv *Conversation) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir := fs.channelDir(conv.ChannelID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create channel dir: %w", err)
	}

	conv.UpdatedAt = time.Now()
	conv.MessageCount = len(conv.Messages)

	// Auto-generate title from first user message
	if conv.Title == "" {
		for _, m := range conv.Messages {
			if m.Role == "user" {
				conv.Title = truncateTitle(m.Content, 60)
				break
			}
		}
		if conv.Title == "" {
			conv.Title = "New conversation"
		}
	}

	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	path := fs.convPath(conv.ChannelID, conv.ID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write conversation: %w", err)
	}

	return nil
}

func (fs *FileStore) Load(channelID, convID string) (*Conversation, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	path := fs.convPath(channelID, convID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // not found
		}
		return nil, fmt.Errorf("failed to read conversation: %w", err)
	}

	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation: %w", err)
	}

	return &conv, nil
}

func (fs *FileStore) Delete(channelID, convID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	path := fs.convPath(channelID, convID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete conversation: %w", err)
	}
	return nil
}

func (fs *FileStore) ListByChannel(channelID string) ([]ConversationSummary, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	dir := fs.channelDir(channelID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	var summaries []ConversationSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var conv Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}

		summaries = append(summaries, ConversationSummary{
			ID:           conv.ID,
			ChannelID:    conv.ChannelID,
			Title:        conv.Title,
			MessageCount: conv.MessageCount,
			CreatedAt:    conv.CreatedAt,
			UpdatedAt:    conv.UpdatedAt,
			ActiveSkill:  conv.ActiveSkill,
		})
	}

	// Sort by most recently updated
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	return summaries, nil
}

func (fs *FileStore) ListChannels() ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	entries, err := os.ReadDir(fs.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}

	var channels []string
	for _, e := range entries {
		if e.IsDir() {
			channels = append(channels, e.Name())
		}
	}
	return channels, nil
}

func (fs *FileStore) Stats(channelID string) (ChannelStats, error) {
	summaries, err := fs.ListByChannel(channelID)
	if err != nil {
		return ChannelStats{}, err
	}

	stats := ChannelStats{
		ChannelID:         channelID,
		ConversationCount: len(summaries),
	}

	for _, s := range summaries {
		stats.TotalMessages += s.MessageCount
	}

	if len(summaries) > 0 {
		// summaries are sorted by UpdatedAt desc
		stats.NewestConv = summaries[0].UpdatedAt.Format(time.RFC3339)
		stats.OldestConv = summaries[len(summaries)-1].CreatedAt.Format(time.RFC3339)
	}

	return stats, nil
}

// --- Utilities ---

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return s
}

func truncateTitle(s string, maxLen int) string {
	// Take first line only
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		s = s[:idx]
	}
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

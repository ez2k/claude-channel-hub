package memory

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

// Memory represents a single persistent memory entry
type Memory struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`      // "fact", "preference", "skill_learned", "context", "episode"
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	Source    string    `json:"source,omitempty"` // which conversation produced this
	Weight    float64   `json:"weight"`           // importance 0.0-1.0
	CreatedAt time.Time `json:"created_at"`
	AccessedAt time.Time `json:"accessed_at"`
	AccessCount int     `json:"access_count"`
}

// RecallResult is a memory retrieval result with relevance score
type RecallResult struct {
	Memory    Memory  `json:"memory"`
	Score     float64 `json:"score"` // 0.0-1.0 relevance
}

// Store provides persistent memory with search
type Store struct {
	baseDir  string
	memories map[string][]Memory // channelID -> memories
	mu       sync.RWMutex
}

func NewStore(baseDir string) (*Store, error) {
	dir := filepath.Join(baseDir, "_memory")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	s := &Store{
		baseDir:  dir,
		memories: make(map[string][]Memory),
	}

	// Load existing memories
	if err := s.loadAll(); err != nil {
		return nil, err
	}

	return s, nil
}

// Save persists a new memory
func (s *Store) Save(mem Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mem.ID == "" {
		mem.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now()
	}
	mem.AccessedAt = mem.CreatedAt
	if mem.Weight == 0 {
		mem.Weight = 0.5
	}

	// Dedup: if very similar memory exists, merge/update instead
	mems := s.memories[mem.ChannelID]
	for i, existing := range mems {
		if existing.Type == mem.Type && similarity(existing.Content, mem.Content) > 0.8 {
			// Update existing
			mems[i].Content = mem.Content
			mems[i].AccessedAt = time.Now()
			mems[i].AccessCount++
			mems[i].Weight = min(1.0, mems[i].Weight+0.1)
			s.memories[mem.ChannelID] = mems
			return s.persist(mem.ChannelID)
		}
	}

	s.memories[mem.ChannelID] = append(s.memories[mem.ChannelID], mem)
	return s.persist(mem.ChannelID)
}

// Recall searches memories relevant to a query
func (s *Store) Recall(channelID, query string, limit int) []RecallResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mems := s.memories[channelID]
	if len(mems) == 0 {
		return nil
	}

	queryWords := tokenize(strings.ToLower(query))
	var results []RecallResult

	for i := range mems {
		score := s.scoreMemory(&mems[i], queryWords)
		if score > 0.1 {
			// Update access
			mems[i].AccessedAt = time.Now()
			mems[i].AccessCount++
			results = append(results, RecallResult{
				Memory: mems[i],
				Score:  score,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

// RecallByType returns memories of a specific type
func (s *Store) RecallByType(channelID, memType string) []Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Memory
	for _, m := range s.memories[channelID] {
		if m.Type == memType {
			results = append(results, m)
		}
	}
	return results
}

// RecallForUser returns all memories for a specific user
func (s *Store) RecallForUser(channelID, userID string, limit int) []Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Memory
	for _, m := range s.memories[channelID] {
		if m.UserID == userID {
			results = append(results, m)
		}
	}

	// Sort by weight * recency
	sort.Slice(results, func(i, j int) bool {
		iScore := results[i].Weight * recencyFactor(results[i].AccessedAt)
		jScore := results[j].Weight * recencyFactor(results[j].AccessedAt)
		return iScore > jScore
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

// FormatForPrompt formats recalled memories into a system prompt section
func FormatForPrompt(memories []RecallResult) string {
	if len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n<recalled_memories>\n")
	sb.WriteString("The following information was recalled from previous conversations:\n")
	for _, r := range memories {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", r.Memory.Type, r.Memory.Content))
	}
	sb.WriteString("</recalled_memories>\n")
	return sb.String()
}

// Stats returns memory statistics for a channel
func (s *Store) Stats(channelID string) map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]int{"total": 0}
	for _, m := range s.memories[channelID] {
		stats["total"]++
		stats[m.Type]++
	}
	return stats
}

// Prune removes low-value memories beyond a threshold
func (s *Store) Prune(channelID string, maxMemories int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	mems := s.memories[channelID]
	if len(mems) <= maxMemories {
		return 0
	}

	// Score all memories
	type scored struct {
		idx   int
		score float64
	}
	var scores []scored
	for i, m := range mems {
		scores = append(scores, scored{
			idx:   i,
			score: m.Weight * recencyFactor(m.AccessedAt) * (1 + float64(m.AccessCount)*0.1),
		})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Keep top N
	keep := make(map[int]bool)
	for i := 0; i < maxMemories && i < len(scores); i++ {
		keep[scores[i].idx] = true
	}

	var pruned []Memory
	removed := 0
	for i, m := range mems {
		if keep[i] {
			pruned = append(pruned, m)
		} else {
			removed++
		}
	}

	s.memories[channelID] = pruned
	s.persist(channelID)
	return removed
}

// --- Persistence ---

func (s *Store) persist(channelID string) error {
	path := filepath.Join(s.baseDir, sanitize(channelID)+".json")
	data, err := json.MarshalIndent(s.memories[channelID], "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Store) loadAll() error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil // empty is fine
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(s.baseDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		channelID := strings.TrimSuffix(e.Name(), ".json")
		var mems []Memory
		if err := json.Unmarshal(data, &mems); err != nil {
			continue
		}
		s.memories[channelID] = mems
	}

	return nil
}

// --- Scoring ---

func (s *Store) scoreMemory(mem *Memory, queryWords []string) float64 {
	contentWords := tokenize(strings.ToLower(mem.Content))
	tagStr := strings.ToLower(strings.Join(mem.Tags, " "))

	// Word overlap score
	matchCount := 0
	for _, qw := range queryWords {
		for _, cw := range contentWords {
			if strings.Contains(cw, qw) || strings.Contains(qw, cw) {
				matchCount++
				break
			}
		}
		if strings.Contains(tagStr, qw) {
			matchCount++
		}
	}

	if len(queryWords) == 0 {
		return 0
	}

	overlapScore := float64(matchCount) / float64(len(queryWords))

	// Boost by weight and recency
	return overlapScore * mem.Weight * recencyFactor(mem.AccessedAt)
}

func recencyFactor(t time.Time) float64 {
	hours := time.Since(t).Hours()
	if hours < 1 {
		return 1.0
	}
	if hours < 24 {
		return 0.9
	}
	if hours < 168 { // 1 week
		return 0.7
	}
	if hours < 720 { // 1 month
		return 0.5
	}
	return 0.3
}

func tokenize(s string) []string {
	var words []string
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, ".,!?;:\"'()[]{}")
		if len(w) > 1 {
			words = append(words, w)
		}
	}
	return words
}

func similarity(a, b string) float64 {
	aWords := tokenize(strings.ToLower(a))
	bWords := tokenize(strings.ToLower(b))
	if len(aWords) == 0 || len(bWords) == 0 {
		return 0
	}

	matches := 0
	for _, aw := range aWords {
		for _, bw := range bWords {
			if aw == bw {
				matches++
				break
			}
		}
	}
	return float64(matches) / float64(max(len(aWords), len(bWords)))
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(s)
}

func min(a, b float64) float64 {
	if a < b { return a }
	return b
}

func max(a, b int) int {
	if a > b { return a }
	return b
}

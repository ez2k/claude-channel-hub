package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Profile represents a user model built across sessions
type Profile struct {
	UserID      string            `json:"user_id"`
	ChannelID   string            `json:"channel_id"`
	Name        string            `json:"name,omitempty"`
	Language    string            `json:"language"`          // detected language
	Preferences map[string]string `json:"preferences"`      // key-value preferences
	Expertise   []string          `json:"expertise"`         // known skills/domains
	Topics      map[string]int    `json:"topics"`            // topic -> mention count
	Style       StyleProfile      `json:"style"`             // communication style
	LastSeen    time.Time         `json:"last_seen"`
	SessionCount int              `json:"session_count"`
	TotalMessages int             `json:"total_messages"`
}

// StyleProfile captures communication preferences
type StyleProfile struct {
	Formality   string `json:"formality"`    // "formal", "casual", "mixed"
	DetailLevel string `json:"detail_level"` // "brief", "detailed", "mixed"
	TechLevel   string `json:"tech_level"`   // "beginner", "intermediate", "advanced"
}

// Store manages user profiles
type Store struct {
	baseDir  string
	profiles map[string]*Profile // "channelID:userID" -> profile
	mu       sync.RWMutex
}

func NewStore(baseDir string) (*Store, error) {
	dir := filepath.Join(baseDir, "_profiles")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	s := &Store{
		baseDir:  dir,
		profiles: make(map[string]*Profile),
	}
	s.loadAll()
	return s, nil
}

func profileKey(channelID, userID string) string {
	return channelID + ":" + userID
}

// Get returns a profile, creating one if needed
func (s *Store) Get(channelID, userID string) *Profile {
	s.mu.RLock()
	p, ok := s.profiles[profileKey(channelID, userID)]
	s.mu.RUnlock()

	if ok {
		return p
	}

	p = &Profile{
		UserID:      userID,
		ChannelID:   channelID,
		Language:    "auto",
		Preferences: make(map[string]string),
		Topics:      make(map[string]int),
		Style: StyleProfile{
			Formality:   "mixed",
			DetailLevel: "mixed",
			TechLevel:   "intermediate",
		},
	}

	s.mu.Lock()
	s.profiles[profileKey(channelID, userID)] = p
	s.mu.Unlock()

	return p
}

// Update modifies a profile and persists it
func (s *Store) Update(p *Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p.LastSeen = time.Now()
	s.profiles[profileKey(p.ChannelID, p.UserID)] = p
	return s.persist(p)
}

// ObserveMessage updates profile from a user message
func (s *Store) ObserveMessage(channelID, userID, message string) {
	p := s.Get(channelID, userID)

	p.TotalMessages++
	p.LastSeen = time.Now()

	// Detect language
	if p.Language == "auto" || p.Language == "" {
		p.Language = detectLanguage(message)
	}

	// Extract topics (simple keyword extraction)
	for _, topic := range extractTopics(message) {
		p.Topics[topic]++
	}

	// Detect style
	updateStyle(&p.Style, message)

	s.Update(p)
}

// FormatForPrompt generates a system prompt section from the profile
func (p *Profile) FormatForPrompt() string {
	if p.TotalMessages < 3 {
		return "" // not enough data yet
	}

	var sb strings.Builder
	sb.WriteString("\n<user_profile>\n")

	if p.Name != "" {
		sb.WriteString(fmt.Sprintf("Name: %s\n", p.Name))
	}
	if p.Language != "auto" && p.Language != "" {
		sb.WriteString(fmt.Sprintf("Preferred language: %s\n", p.Language))
	}

	sb.WriteString(fmt.Sprintf("Communication style: %s formality, %s detail, %s technical level\n",
		p.Style.Formality, p.Style.DetailLevel, p.Style.TechLevel))

	if len(p.Expertise) > 0 {
		sb.WriteString(fmt.Sprintf("Known expertise: %s\n", strings.Join(p.Expertise, ", ")))
	}

	// Top topics
	topTopics := topN(p.Topics, 5)
	if len(topTopics) > 0 {
		sb.WriteString(fmt.Sprintf("Frequent topics: %s\n", strings.Join(topTopics, ", ")))
	}

	// Preferences
	for k, v := range p.Preferences {
		sb.WriteString(fmt.Sprintf("Preference — %s: %s\n", k, v))
	}

	sb.WriteString(fmt.Sprintf("Sessions: %d, Messages: %d\n", p.SessionCount, p.TotalMessages))
	sb.WriteString("</user_profile>\n")
	sb.WriteString("Adapt your responses to match this user's style and expertise level.\n")

	return sb.String()
}

// --- Persistence ---

func (s *Store) persist(p *Profile) error {
	path := filepath.Join(s.baseDir, sanitize(p.ChannelID+"_"+p.UserID)+".json")
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Store) loadAll() {
	entries, _ := os.ReadDir(s.baseDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.baseDir, e.Name()))
		if err != nil {
			continue
		}
		var p Profile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		s.profiles[profileKey(p.ChannelID, p.UserID)] = &p
	}
}

// --- Analysis helpers ---

func detectLanguage(text string) string {
	koreanCount := 0
	totalChars := 0
	for _, r := range text {
		totalChars++
		if r >= 0xAC00 && r <= 0xD7AF { // Hangul syllables
			koreanCount++
		}
	}
	if totalChars > 0 && float64(koreanCount)/float64(totalChars) > 0.2 {
		return "ko"
	}

	japaneseCount := 0
	for _, r := range text {
		if (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) {
			japaneseCount++
		}
	}
	if totalChars > 0 && float64(japaneseCount)/float64(totalChars) > 0.1 {
		return "ja"
	}

	return "en"
}

func extractTopics(text string) []string {
	// Simple keyword extraction for common tech/work topics
	keywords := map[string][]string{
		"coding":     {"코드", "code", "programming", "개발", "함수", "function", "버그", "bug"},
		"devops":     {"docker", "kubernetes", "k8s", "deploy", "배포", "ci/cd", "서버"},
		"ai":         {"ai", "ml", "모델", "model", "llm", "gpt", "claude", "에이전트", "agent"},
		"database":   {"db", "sql", "database", "데이터베이스", "쿼리", "query"},
		"web":        {"html", "css", "react", "frontend", "backend", "api", "웹"},
		"mobile":     {"ios", "android", "앱", "app", "모바일", "mobile"},
		"security":   {"보안", "security", "인증", "auth", "토큰", "token"},
		"automation": {"자동화", "automation", "스크립트", "script", "워크플로우", "workflow"},
	}

	lower := strings.ToLower(text)
	var found []string
	for topic, words := range keywords {
		for _, w := range words {
			if strings.Contains(lower, w) {
				found = append(found, topic)
				break
			}
		}
	}
	return found
}

func updateStyle(style *StyleProfile, text string) {
	lower := strings.ToLower(text)

	// Formality detection
	if strings.Contains(lower, "합니다") || strings.Contains(lower, "입니다") || strings.Contains(lower, "please") {
		style.Formality = "formal"
	} else if strings.Contains(lower, "해줘") || strings.Contains(lower, "ㅋㅋ") || strings.Contains(lower, "lol") {
		style.Formality = "casual"
	}

	// Tech level
	techTerms := []string{"api", "sdk", "docker", "kubernetes", "goroutine", "mutex", "concurrency", "microservice"}
	techCount := 0
	for _, t := range techTerms {
		if strings.Contains(lower, t) {
			techCount++
		}
	}
	if techCount >= 2 {
		style.TechLevel = "advanced"
	} else if techCount >= 1 {
		style.TechLevel = "intermediate"
	}
}

func topN(m map[string]int, n int) []string {
	type kv struct{ k string; v int }
	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	var result []string
	for i := 0; i < n && i < len(sorted); i++ {
		result = append(result, sorted[i].k)
	}
	return result
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_").Replace(s)
}

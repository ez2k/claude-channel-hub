package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ez2k/claude-channel-hub/internal/memory"
	"github.com/ez2k/claude-channel-hub/internal/profile"
	"github.com/ez2k/claude-channel-hub/internal/provider"
	"github.com/ez2k/claude-channel-hub/internal/skills"
	"github.com/ez2k/claude-channel-hub/internal/store"
	"github.com/ez2k/claude-channel-hub/internal/tools"
)

type Agent struct {
	provider      provider.Provider
	systemPrompt  string
	maxIterations int
	toolRegistry  *tools.Registry
	skillRegistry *skills.Registry
	skillEvolver  *skills.Evolver
	memoryStore   *memory.Store
	profileStore  *profile.Store
}

type Session struct {
	ActiveSkill *skills.Skill
	CreatedAt   time.Time
	Log         []store.Message
	// Internal message history for provider
	messages []provider.Message
}

type Config struct {
	SystemPrompt  string
	MaxIterations int
}

func New(cfg Config, prov provider.Provider, toolReg *tools.Registry, skillReg *skills.Registry,
	evolver *skills.Evolver, memStore *memory.Store, profStore *profile.Store) *Agent {

	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 15
	}
	return &Agent{
		provider:      prov,
		systemPrompt:  cfg.SystemPrompt,
		maxIterations: cfg.MaxIterations,
		toolRegistry:  toolReg,
		skillRegistry: skillReg,
		skillEvolver:  evolver,
		memoryStore:   memStore,
		profileStore:  profStore,
	}
}

func NewSession() *Session {
	return &Session{
		Log:       []store.Message{},
		messages:  []provider.Message{},
		CreatedAt: time.Now(),
	}
}

func RestoreFromConversation(conv *store.Conversation, skillReg *skills.Registry) *Session {
	session := &Session{
		Log:       conv.Messages,
		messages:  []provider.Message{},
		CreatedAt: conv.CreatedAt,
	}
	for _, m := range conv.Messages {
		session.messages = append(session.messages, provider.Message{
			Role: m.Role, Content: m.Content,
		})
	}
	if conv.ActiveSkill != "" && skillReg != nil {
		if skill, ok := skillReg.Get(conv.ActiveSkill); ok {
			session.ActiveSkill = skill
		}
	}
	return session
}

func (s *Session) ToConversation(channelID, convID string) *store.Conversation {
	skillName := ""
	if s.ActiveSkill != nil {
		skillName = s.ActiveSkill.Name
	}
	return &store.Conversation{
		ID: convID, ChannelID: channelID,
		Messages: s.Log, ActiveSkill: skillName,
		CreatedAt: s.CreatedAt, UpdatedAt: time.Now(),
		MessageCount: len(s.Log),
	}
}

// Run executes the agentic loop
func (a *Agent) Run(_ context.Context, session *Session, channelID, userID, userMessage string) (string, error) {
	// 1. Observe user for profiling
	if a.profileStore != nil {
		a.profileStore.ObserveMessage(channelID, userID, userMessage)
	}

	// 2. Log user message
	session.messages = append(session.messages, provider.Message{Role: "user", Content: userMessage})
	session.Log = append(session.Log, store.Message{
		Role: "user", Content: userMessage, Timestamp: time.Now(),
	})

	// 3. Build system prompt
	sysPrompt := a.buildSystemPrompt(session, channelID, userID, userMessage)

	// 4. Build tool definitions
	toolDefs := a.buildToolDefs()

	// 5. Provider-dependent loop
	if a.provider.Mode() == "cli" {
		return a.runCLI(session, sysPrompt, channelID, userID, userMessage)
	}
	return a.runAPI(session, sysPrompt, toolDefs, channelID, userID, userMessage)
}

// runCLI sends a single request through claude -p (no tool loop needed, CLI handles tools)
func (a *Agent) runCLI(session *Session, sysPrompt, channelID, userID, userMessage string) (string, error) {
	resp, err := a.provider.Send(sysPrompt, session.messages, nil)
	if err != nil {
		return "", err
	}

	// Log assistant response
	session.messages = append(session.messages, provider.Message{Role: "assistant", Content: resp.Text})
	session.Log = append(session.Log, store.Message{
		Role: "assistant", Content: resp.Text, Timestamp: time.Now(),
	})

	a.postProcess(channelID, userID, userMessage, resp.Text)
	return resp.Text, nil
}

// runAPI handles the full agentic tool-use loop via direct API
func (a *Agent) runAPI(session *Session, sysPrompt string, toolDefs []provider.ToolDef,
	channelID, userID, userMessage string) (string, error) {

	for i := 0; i < a.maxIterations; i++ {
		resp, err := a.provider.Send(sysPrompt, session.messages, toolDefs)
		if err != nil {
			return "", err
		}

		// If done (no tool calls)
		if resp.Done || len(resp.ToolUses) == 0 {
			session.messages = append(session.messages, provider.Message{Role: "assistant", Content: resp.Text})
			session.Log = append(session.Log, store.Message{
				Role: "assistant", Content: resp.Text, Timestamp: time.Now(),
			})
			a.postProcess(channelID, userID, userMessage, resp.Text)
			return resp.Text, nil
		}

		// Process tool calls
		var toolLog []store.ToolUse
		var toolResultParts []string

		// Add assistant message with tool calls
		session.messages = append(session.messages, provider.Message{Role: "assistant", Content: resp.Text})

		for _, tc := range resp.ToolUses {
			var input map[string]interface{}
			json.Unmarshal([]byte(tc.Input), &input)

			tool, ok := a.toolRegistry.Get(tc.Name)
			if !ok {
				toolResultParts = append(toolResultParts, fmt.Sprintf("[%s] Error: unknown tool", tc.Name))
				continue
			}

			fmt.Printf("  🔧 %s\n", tc.Name)
			output, err := tool.Execute(input)
			entry := store.ToolUse{Name: tc.Name, Input: tc.Input}

			if err != nil {
				msg := fmt.Sprintf("Error: %v", err)
				toolResultParts = append(toolResultParts, fmt.Sprintf("[%s] %s", tc.Name, msg))
				entry.Output = msg
			} else {
				toolResultParts = append(toolResultParts, fmt.Sprintf("[%s] %s", tc.Name, truncate(output, 2000)))
				entry.Output = truncate(output, 500)
			}
			toolLog = append(toolLog, entry)
		}

		// Log intermediate
		session.Log = append(session.Log, store.Message{
			Role: "assistant", Content: resp.Text, Timestamp: time.Now(), ToolUse: toolLog,
		})

		// Add tool results as user message
		resultMsg := "Tool results:\n" + strings.Join(toolResultParts, "\n")
		session.messages = append(session.messages, provider.Message{Role: "user", Content: resultMsg})
	}

	return "", fmt.Errorf("exceeded max iterations (%d)", a.maxIterations)
}

// buildSystemPrompt combines all prompt sources
func (a *Agent) buildSystemPrompt(session *Session, channelID, userID, query string) string {
	var sb strings.Builder
	sb.WriteString(a.systemPrompt)
	sb.WriteString(a.skillRegistry.BuildSystemPrompt())

	if session.ActiveSkill != nil {
		sb.WriteString(fmt.Sprintf("\n\n<active_skill name=\"%s\">\n%s\n</active_skill>\n",
			session.ActiveSkill.Name, session.ActiveSkill.Content))
	}

	if a.memoryStore != nil {
		recalled := a.memoryStore.Recall(channelID, query, 5)
		if len(recalled) > 0 {
			sb.WriteString(memory.FormatForPrompt(recalled))
		}
	}

	if a.profileStore != nil {
		prof := a.profileStore.Get(channelID, userID)
		sb.WriteString(prof.FormatForPrompt())
	}

	if a.skillEvolver != nil {
		sb.WriteString(a.skillEvolver.GenerateCreatePrompt())
	}

	return sb.String()
}

func (a *Agent) buildToolDefs() []provider.ToolDef {
	var defs []provider.ToolDef
	for _, t := range a.toolRegistry.List() {
		defs = append(defs, provider.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return defs
}

func (a *Agent) postProcess(channelID, userID, userMessage, response string) {
	if a.memoryStore != nil {
		a.extractMemories(channelID, userID, userMessage, response)
	}
	if a.skillEvolver != nil {
		if candidate := a.skillEvolver.ParseSkillCreation(response); candidate != nil {
			candidate.SourceConv = channelID + "/" + userID
			a.skillEvolver.CreateFromExperience(*candidate)
		}
	}
}

func (a *Agent) extractMemories(channelID, userID, userMsg, response string) {
	lower := strings.ToLower(userMsg)

	prefPhrases := []string{"prefer", "like", "want", "좋아", "선호", "원해"}
	for _, p := range prefPhrases {
		if strings.Contains(lower, p) {
			a.memoryStore.Save(memory.Memory{
				ChannelID: channelID, UserID: userID,
				Type: "preference", Content: userMsg, Weight: 0.7,
			})
			break
		}
	}

	contextPhrases := []string{"i am", "i work", "my project", "나는", "저는", "내 프로젝트"}
	for _, p := range contextPhrases {
		if strings.Contains(lower, p) {
			a.memoryStore.Save(memory.Memory{
				ChannelID: channelID, UserID: userID,
				Type: "context", Content: userMsg, Weight: 0.8,
			})
			break
		}
	}

	correctionPhrases := []string{"no,", "wrong", "아니", "틀렸", "그게 아니라"}
	for _, p := range correctionPhrases {
		if strings.Contains(lower, p) {
			a.memoryStore.Save(memory.Memory{
				ChannelID: channelID, UserID: userID,
				Type: "fact", Content: fmt.Sprintf("Correction: %s", truncate(userMsg, 200)),
				Weight: 0.9,
			})
			break
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}

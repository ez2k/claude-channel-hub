package channel

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/your-org/claude-harness/internal/agent"
	"github.com/your-org/claude-harness/internal/memory"
	"github.com/your-org/claude-harness/internal/profile"
	"github.com/your-org/claude-harness/internal/skills"
	"github.com/your-org/claude-harness/internal/store"
)

// AgentFactory creates a new agent instance
type AgentFactory func(model string) *agent.Agent

// TelegramChannel implements Channel for Telegram with conversation persistence
type TelegramChannel struct {
	id      string
	name    string
	token   string
	model   string

	api           *tgbotapi.BotAPI
	agentFactory  AgentFactory
	skillRegistry *skills.Registry
	store         store.Store
	memoryStore   *memory.Store
	profileStore  *profile.Store
	marketplace   *skills.Marketplace
	metrics       *Metrics

	// In-memory sessions (hot cache, backed by store)
	sessions map[int64]*agent.Session
	mu       sync.RWMutex

	cancel context.CancelFunc
}

func NewTelegramChannel(cfg ChannelConfig, factory AgentFactory, sr *skills.Registry, st store.Store, ms *memory.Store, ps *profile.Store, mp *skills.Marketplace) (*TelegramChannel, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("telegram token is required for channel %s", cfg.ID)
	}

	return &TelegramChannel{
		id:            cfg.ID,
		name:          cfg.Name,
		token:         cfg.Token,
		model:         cfg.Model,
		agentFactory:  factory,
		skillRegistry: sr,
		store:         st,
		memoryStore:   ms,
		profileStore:  ps,
		marketplace:   mp,
		metrics:       NewMetrics(),
		sessions:      make(map[int64]*agent.Session),
	}, nil
}

func (t *TelegramChannel) ID() string             { return t.id }
func (t *TelegramChannel) Type() string            { return "telegram" }
func (t *TelegramChannel) Name() string            { return t.name }
func (t *TelegramChannel) GetMetrics() *Metrics    { return t.metrics }
func (t *TelegramChannel) GetStore() store.Store   { return t.store }

func (t *TelegramChannel) Start(ctx context.Context) error {
	t.metrics.SetStatus(StatusStarting)

	api, err := tgbotapi.NewBotAPI(t.token)
	if err != nil {
		t.metrics.RecordError(err)
		return fmt.Errorf("telegram auth failed: %w", err)
	}
	t.api = api

	if err := t.registerCommands(); err != nil {
		log.Printf("⚠️  [%s] Failed to register commands: %v", t.id, err)
	}

	t.metrics.SetStatus(StatusRunning)
	log.Printf("✅ [%s] Telegram bot started: @%s", t.id, api.Self.UserName)

	childCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := t.api.GetUpdatesChan(u)

	for {
		select {
		case <-childCtx.Done():
			t.api.StopReceivingUpdates()
			t.saveAllSessions() // persist on shutdown
			t.metrics.SetStatus(StatusStopped)
			return nil
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			t.metrics.IncrMessages()
			t.metrics.SetSessions(len(t.sessions))
			go t.handleMessage(childCtx, update.Message)
		}
	}
}

func (t *TelegramChannel) Stop() error {
	t.metrics.SetStatus(StatusStopping)
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (t *TelegramChannel) Ping() error {
	if t.api == nil {
		return fmt.Errorf("bot not initialized")
	}
	start := time.Now()
	_, err := t.api.GetMe()
	if err != nil {
		t.metrics.RecordError(err)
		return err
	}
	t.metrics.RecordPing(time.Since(start))
	return nil
}

// --- Commands ---

func (t *TelegramChannel) registerCommands() error {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "🚀 봇 시작"},
		{Command: "help", Description: "📖 도움말"},
		{Command: "reset", Description: "🔄 대화 초기화 (새 대화 시작)"},
		{Command: "history", Description: "📜 이전 대화 목록"},
		{Command: "load", Description: "📂 이전 대화 불러오기 (예: /load 12345)"},
		{Command: "skills", Description: "📋 스킬 목록"},
		{Command: "status", Description: "📊 봇 상태"},
		{Command: "memory", Description: "🧠 기억된 정보 조회"},
		{Command: "profile", Description: "👤 내 프로필 보기"},
		{Command: "search", Description: "🔍 스킬 마켓 검색 (예: /search react)"},
		{Command: "install", Description: "📦 스킬 설치 (예: /install owner/repo/skill)"},
	}

	for _, skill := range t.skillRegistry.List() {
		commands = append(commands, tgbotapi.BotCommand{
			Command:     skill.Command,
			Description: truncate(skill.Description, 256),
		})
	}

	cfg := tgbotapi.NewSetMyCommands(commands...)
	_, err := t.api.Request(cfg)
	return err
}

func (t *TelegramChannel) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			t.sendText(chatID,
				"👋 안녕하세요! Claude 에이전트입니다.\n\n"+
					"💬 메시지를 보내면 AI가 답변합니다.\n"+
					"/skills — 스킬 목록\n"+
					"/history — 이전 대화 보기\n"+
					"/reset — 새 대화 시작")
			return

		case "help":
			t.sendHelp(chatID)
			return

		case "reset":
			// Save current session before resetting
			t.saveSession(chatID)
			t.mu.Lock()
			delete(t.sessions, chatID)
			t.mu.Unlock()
			t.sendText(chatID, "🔄 새로운 대화를 시작합니다.\n이전 대화는 /history 에서 확인할 수 있습니다.")
			return

		case "history":
			t.showHistory(chatID)
			return

		case "load":
			args := msg.CommandArguments()
			if args == "" {
				t.sendText(chatID, "사용법: /load <대화ID>\n\n/history 에서 ID를 확인하세요.")
				return
			}
			t.loadConversation(chatID, strings.TrimSpace(args))
			return

		case "skills":
			t.sendSkillList(chatID)
			return

		case "status":
			info := t.metrics.Snapshot(t.id, t.Type(), t.name)
			stats, _ := t.store.Stats(t.id)
			t.sendText(chatID, fmt.Sprintf(
				"📊 *봇 상태*\n\n"+
					"채널: %s\n"+
					"상태: %s\n"+
					"업타임: %s\n"+
					"재시작: %d회\n"+
					"활성 세션: %d개\n"+
					"총 메시지: %d건\n\n"+
					"💾 *저장된 대화*\n"+
					"대화 수: %d개\n"+
					"총 메시지: %d건",
				info.Name, info.Status, info.Uptime,
				info.RestartCount, info.SessionCount, info.MessageCount,
				stats.ConversationCount, stats.TotalMessages,
			))
			return

		case "memory":
			t.showMemory(chatID)
			return

		case "profile":
			t.showProfile(chatID)
			return

		case "search":
			args := msg.CommandArguments()
			if args == "" {
				t.sendText(chatID, "사용법: `/search 키워드`\n\n예: `/search react`, `/search docker`, `/search python testing`")
				return
			}
			t.searchSkills(chatID, args)
			return

		case "install":
			args := msg.CommandArguments()
			if args == "" {
				t.sendText(chatID, "사용법: `/install owner/repo/skill_name`\n\n예: `/install vercel-labs/agent-skills/react-best-practices`\n\n`/search`로 먼저 검색하세요.")
				return
			}
			t.installSkill(chatID, args)
			return

		default:
			if skill, ok := t.skillRegistry.Get(msg.Command()); ok {
				t.activateSkill(chatID, skill, msg.CommandArguments())
				return
			}
		}
	}

	t.runAgent(ctx, chatID, text)
}

// --- Conversation management ---

func (t *TelegramChannel) showHistory(chatID int64) {
	convID := strconv.FormatInt(chatID, 10)
	summaries, err := t.store.ListByChannel(t.id)
	if err != nil {
		t.sendText(chatID, "⚠️ 대화 목록을 불러올 수 없습니다.")
		return
	}

	if len(summaries) == 0 {
		t.sendText(chatID, "📜 저장된 대화가 없습니다.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📜 *이전 대화 목록*\n\n")

	count := 0
	for _, s := range summaries {
		// Show conversations for this chat
		if !strings.HasPrefix(s.ID, convID) && s.ID != convID {
			continue
		}
		count++
		if count > 20 {
			sb.WriteString("...\n")
			break
		}

		timeStr := s.UpdatedAt.Format("01/02 15:04")
		skillTag := ""
		if s.ActiveSkill != "" {
			skillTag = " 🎯" + s.ActiveSkill
		}
		sb.WriteString(fmt.Sprintf(
			"`%s` %s (%d건)%s\n  📎 _%s_\n\n",
			s.ID, timeStr, s.MessageCount, skillTag, s.Title,
		))
	}

	// If no conversations for this specific chat, show all
	if count == 0 {
		count2 := 0
		for _, s := range summaries {
			count2++
			if count2 > 20 {
				break
			}
			timeStr := s.UpdatedAt.Format("01/02 15:04")
			sb.WriteString(fmt.Sprintf(
				"`%s` %s (%d건)\n  📎 _%s_\n\n",
				s.ID, timeStr, s.MessageCount, s.Title,
			))
		}
	}

	sb.WriteString("대화를 불러오려면: /load <ID>")
	t.sendText(chatID, sb.String())
}

func (t *TelegramChannel) loadConversation(chatID int64, convID string) {
	conv, err := t.store.Load(t.id, convID)
	if err != nil || conv == nil {
		t.sendText(chatID, fmt.Sprintf("⚠️ 대화 `%s`을(를) 찾을 수 없습니다.", convID))
		return
	}

	// Save current session first
	t.saveSession(chatID)

	// Restore session from conversation
	session := agent.RestoreFromConversation(conv, t.skillRegistry)

	t.mu.Lock()
	t.sessions[chatID] = session
	t.mu.Unlock()

	skillInfo := ""
	if conv.ActiveSkill != "" {
		skillInfo = fmt.Sprintf("\n🎯 스킬: %s", conv.ActiveSkill)
	}

	t.sendText(chatID, fmt.Sprintf(
		"📂 대화를 불러왔습니다.\n\n"+
			"📎 %s\n"+
			"💬 메시지: %d건%s\n\n"+
			"이어서 대화하세요!",
		conv.Title, conv.MessageCount, skillInfo,
	))
}

func (t *TelegramChannel) activateSkill(chatID int64, skill *skills.Skill, args string) {
	session := t.getOrCreateSession(chatID)
	t.mu.Lock()
	session.ActiveSkill = skill
	t.mu.Unlock()

	t.sendText(chatID, fmt.Sprintf(
		"🎯 *%s* 스킬 활성화\n_%s_\n\n메시지를 보내세요. /reset 으로 해제.",
		skill.Name, skill.Description,
	))

	if args != "" {
		t.runAgent(context.Background(), chatID, args)
	}
}

// --- Agent execution ---

func (t *TelegramChannel) runAgent(ctx context.Context, chatID int64, userMessage string) {
	session := t.getOrCreateSession(chatID)
	userID := strconv.FormatInt(chatID, 10)

	typing := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	t.api.Send(typing)

	ag := t.agentFactory(t.model)
	response, err := ag.Run(ctx, session, t.id, userID, userMessage)
	if err != nil {
		log.Printf("❌ [%s] Agent error chat=%d: %v", t.id, chatID, err)
		t.sendText(chatID, fmt.Sprintf("⚠️ 오류: %v", err))
		return
	}

	// Persist conversation after each exchange
	t.saveSession(chatID)

	t.sendLongText(chatID, response)
}

// --- Session management with store ---

func (t *TelegramChannel) getOrCreateSession(chatID int64) *agent.Session {
	t.mu.RLock()
	session, ok := t.sessions[chatID]
	t.mu.RUnlock()

	if ok {
		return session
	}

	convID := t.convID(chatID)

	// Try to load from store
	conv, err := t.store.Load(t.id, convID)
	if err == nil && conv != nil {
		session = agent.RestoreFromConversation(conv, t.skillRegistry)
		log.Printf("📂 [%s] Restored conversation %s (%d messages)", t.id, convID, conv.MessageCount)
	} else {
		session = agent.NewSession()
	}

	t.mu.Lock()
	t.sessions[chatID] = session
	t.mu.Unlock()

	return session
}

func (t *TelegramChannel) saveSession(chatID int64) {
	t.mu.RLock()
	session, ok := t.sessions[chatID]
	t.mu.RUnlock()

	if !ok || len(session.Log) == 0 {
		return
	}

	convID := t.convID(chatID)
	conv := session.ToConversation(t.id, convID)

	if err := t.store.Save(conv); err != nil {
		log.Printf("⚠️ [%s] Failed to save conversation %s: %v", t.id, convID, err)
	}
}

func (t *TelegramChannel) saveAllSessions() {
	t.mu.RLock()
	chatIDs := make([]int64, 0, len(t.sessions))
	for id := range t.sessions {
		chatIDs = append(chatIDs, id)
	}
	t.mu.RUnlock()

	for _, chatID := range chatIDs {
		t.saveSession(chatID)
	}
	log.Printf("💾 [%s] Saved %d sessions", t.id, len(chatIDs))
}

func (t *TelegramChannel) convID(chatID int64) string {
	return strconv.FormatInt(chatID, 10)
}

// --- Skill Marketplace ---

func (t *TelegramChannel) searchSkills(chatID int64, query string) {
	if t.marketplace == nil {
		t.sendText(chatID, "⚠️ 스킬 마켓이 비활성화되어 있습니다.")
		return
	}

	t.sendText(chatID, fmt.Sprintf("🔍 '%s' 검색 중...", query))

	result, err := t.marketplace.Search(query)
	if err != nil {
		t.sendText(chatID, fmt.Sprintf("⚠️ 검색 실패: %v", err))
		return
	}

	t.sendLongText(chatID, skills.FormatSearchResults(result))
}

func (t *TelegramChannel) installSkill(chatID int64, source string) {
	if t.marketplace == nil {
		t.sendText(chatID, "⚠️ 스킬 마켓이 비활성화되어 있습니다.")
		return
	}

	t.sendText(chatID, fmt.Sprintf("📦 스킬 설치 중: `%s`...", source))

	parts := strings.Split(source, "/")
	skillName := ""
	if len(parts) >= 3 {
		skillName = parts[len(parts)-1]
	}

	if err := t.marketplace.Install(source, skillName); err != nil {
		t.sendText(chatID, fmt.Sprintf("❌ 설치 실패: %v", err))
		return
	}

	// Reload skills registry
	if err := t.skillRegistry.LoadFromDir(filepath.Dir(t.marketplace.GetInstallDir())); err != nil {
		log.Printf("⚠️ Skills reload: %v", err)
	}

	// Re-register Telegram commands with new skill
	t.registerCommands()

	t.sendText(chatID, fmt.Sprintf("✅ *%s* 스킬이 설치되었습니다!\n\n"+
		"스킬이 메뉴에 자동 등록되었습니다.\n"+
		"/skills 로 확인하세요.", skillName))
}

// --- Memory and Profile ---

func (t *TelegramChannel) showMemory(chatID int64) {
	if t.memoryStore == nil {
		t.sendText(chatID, "🧠 메모리 시스템이 비활성화되어 있습니다.")
		return
	}

	userID := strconv.FormatInt(chatID, 10)
	memories := t.memoryStore.RecallForUser(t.id, userID, 15)
	stats := t.memoryStore.Stats(t.id)

	if len(memories) == 0 {
		t.sendText(chatID, "🧠 아직 기억된 정보가 없습니다.\n대화를 더 나누면 자동으로 학습합니다.")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧠 *기억된 정보* (총 %d건)\n\n", stats["total"]))

	for i, m := range memories {
		if i >= 10 {
			sb.WriteString("...\n")
			break
		}
		icon := "💡"
		switch m.Type {
		case "preference":
			icon = "❤️"
		case "context":
			icon = "📌"
		case "fact":
			icon = "✅"
		case "skill_learned":
			icon = "🎓"
		}
		content := m.Content
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s _%s_\n", icon, content))
	}

	sb.WriteString(fmt.Sprintf("\n📊 타입별: fact %d, preference %d, context %d",
		stats["fact"], stats["preference"], stats["context"]))
	t.sendText(chatID, sb.String())
}

func (t *TelegramChannel) showProfile(chatID int64) {
	if t.profileStore == nil {
		t.sendText(chatID, "👤 프로필 시스템이 비활성화되어 있습니다.")
		return
	}

	userID := strconv.FormatInt(chatID, 10)
	p := t.profileStore.Get(t.id, userID)

	var sb strings.Builder
	sb.WriteString("👤 *내 프로필*\n\n")

	if p.Name != "" {
		sb.WriteString(fmt.Sprintf("이름: %s\n", p.Name))
	}
	sb.WriteString(fmt.Sprintf("언어: %s\n", p.Language))
	sb.WriteString(fmt.Sprintf("스타일: %s / %s / %s\n",
		p.Style.Formality, p.Style.DetailLevel, p.Style.TechLevel))
	sb.WriteString(fmt.Sprintf("세션: %d회 / 메시지: %d건\n", p.SessionCount, p.TotalMessages))

	if len(p.Topics) > 0 {
		var topics []string
		for topic, count := range p.Topics {
			topics = append(topics, fmt.Sprintf("%s(%d)", topic, count))
		}
		sb.WriteString(fmt.Sprintf("\n📊 관심 주제: %s", strings.Join(topics, ", ")))
	}

	if len(p.Expertise) > 0 {
		sb.WriteString(fmt.Sprintf("\n🎯 전문분야: %s", strings.Join(p.Expertise, ", ")))
	}

	sb.WriteString("\n\n_대화를 나눌수록 프로필이 자동으로 발전합니다._")
	t.sendText(chatID, sb.String())
}

// --- Send helpers ---

func (t *TelegramChannel) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := t.api.Send(msg); err != nil {
		msg.ParseMode = ""
		t.api.Send(msg)
	}
}

func (t *TelegramChannel) sendLongText(chatID int64, text string) {
	const maxLen = 4000
	if len(text) <= maxLen {
		t.sendText(chatID, text)
		return
	}
	for _, chunk := range splitText(text, maxLen) {
		t.sendText(chatID, chunk)
	}
}

func (t *TelegramChannel) sendHelp(chatID int64) {
	var sb strings.Builder
	sb.WriteString("📖 *명령어*\n\n")
	sb.WriteString("/reset — 새 대화 시작\n")
	sb.WriteString("/history — 이전 대화 목록\n")
	sb.WriteString("/load <ID> — 이전 대화 불러오기\n")
	sb.WriteString("/skills — 스킬 목록\n")
	sb.WriteString("/status — 봇 상태\n\n")

	if allSkills := t.skillRegistry.List(); len(allSkills) > 0 {
		sb.WriteString("🎯 *스킬*\n\n")
		for _, s := range allSkills {
			sb.WriteString(fmt.Sprintf("/%s — %s\n", s.Command, s.Description))
		}
	}
	t.sendText(chatID, sb.String())
}

func (t *TelegramChannel) sendSkillList(chatID int64) {
	allSkills := t.skillRegistry.List()
	if len(allSkills) == 0 {
		t.sendText(chatID, "📋 등록된 스킬이 없습니다.")
		return
	}
	var sb strings.Builder
	sb.WriteString("📋 *스킬 목록*\n\n")
	for _, s := range allSkills {
		sb.WriteString(fmt.Sprintf("🔹 /%s — %s\n", s.Command, s.Description))
	}
	t.sendText(chatID, sb.String())
}

// --- Utilities ---

func splitText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	paragraphs := strings.Split(text, "\n\n")
	var current strings.Builder
	for _, p := range paragraphs {
		if current.Len()+len(p)+2 > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			if len(p) > maxLen {
				for i := 0; i < len(p); i += maxLen {
					end := i + maxLen
					if end > len(p) {
						end = len(p)
					}
					chunks = append(chunks, p[i:end])
				}
				continue
			}
			current.WriteString(p)
		} else {
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(p)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

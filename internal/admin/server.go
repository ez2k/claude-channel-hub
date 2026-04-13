package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ez2k/claude-channel-hub/internal/bot"
	"github.com/ez2k/claude-channel-hub/internal/config"
	"github.com/ez2k/claude-channel-hub/internal/store"
	"github.com/ez2k/claude-channel-hub/internal/supervisor"
	"github.com/ez2k/claude-channel-hub/internal/version"
)

type Server struct {
	sv         *supervisor.Supervisor
	store      store.Store
	versionMgr *version.Manager
	cfg        *config.Root
	configPath string
	addr       string
	httpClient *http.Client
}

func NewServer(addr string, sv *supervisor.Supervisor, st store.Store, vm *version.Manager, cfg *config.Root, configPath string) *Server {
	return &Server{
		sv:         sv,
		store:      st,
		versionMgr: vm,
		cfg:        cfg,
		configPath: configPath,
		addr:       addr,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Bot management
	mux.HandleFunc("/api/bots", s.handleBots)
	mux.HandleFunc("/api/bots/", s.handleBotAction)

	// Channel listing
	mux.HandleFunc("/api/channels", s.handleChannels)

	// Version info
	mux.HandleFunc("/api/versions", s.handleVersions)
	mux.HandleFunc("/api/versions/", s.handleVersions)

	// Config
	mux.HandleFunc("/api/config", s.handleConfig)

	// Access control is per-bot, handled via /api/bots/:id/access and /api/bots/:id/access/detect

	// System
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/health", s.handleHealth)

	// Conversations (keep existing)
	mux.HandleFunc("/api/conversations/", s.handleConversations)

	// Dashboard
	mux.HandleFunc("/", s.handleDashboard)

	srv := &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("🌐 Admin dashboard: http://localhost%s", s.addr)
	return srv.ListenAndServe()
}

// --- Bot API ---

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		var req config.BotConfig
		json.Unmarshal(body, &req)
		if req.ID == "" || req.Token == "" {
			http.Error(w, "id and token required", 400)
			return
		}
		if req.Type == "" {
			req.Type = "telegram"
		}
		if req.Plugin == "" {
			req.Plugin = req.Type
		}
		if req.PluginMarketplace == "" {
			req.PluginMarketplace = "claude-plugins-official"
		}
		req.Enabled = true

		// Check duplicate
		if s.cfg != nil {
			for _, b := range s.cfg.Bots {
				if b.ID == req.ID {
					http.Error(w, "bot ID already exists", 409)
					return
				}
			}
			s.cfg.Bots = append(s.cfg.Bots, req)
			config.Save(s.configPath, s.cfg)
		}

		// Create and start bot
		b := &bot.Bot{Config: bot.BotConfig{
			ID:                req.ID,
			Type:              req.Type,
			Name:              req.Name,
			Enabled:           req.Enabled,
			Token:             req.Token,
			Plugin:            req.Plugin,
			PluginMarketplace: req.PluginMarketplace,
			Model:             req.Model,
			SystemPrompt:      req.SystemPrompt,
		}}
		s.sv.RegisterAndStart(b)
		writeJSON(w, map[string]string{"status": "created", "bot": req.ID})
		return
	}

	bots := s.sv.Status()
	writeJSON(w, map[string]interface{}{"bots": bots})
}

func (s *Server) handleBotAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/bots/")
	parts := splitPath(strings.Trim(path, "/"))

	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, "missing bot id", 400)
		return
	}

	botID := parts[0]

	// GET /api/bots/:id
	if r.Method == http.MethodGet && len(parts) == 1 {
		bots := s.sv.Status()
		for _, b := range bots {
			if b.ID == botID {
				writeJSON(w, b)
				return
			}
		}
		http.Error(w, "bot not found", 404)
		return
	}

	// GET /api/bots/:id/channels
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "channels" {
		channels := s.sv.ChannelsForBot(botID)
		writeJSON(w, map[string]interface{}{"channels": channels})
		return
	}

	// POST /api/bots/:id/restart
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "restart" {
		if err := s.sv.RestartBot(botID); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, map[string]string{"status": "restart_requested", "bot": botID})
		return
	}

	// GET /api/bots/:id/logs?lines=200
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "logs" {
		lines := 200
		if l := r.URL.Query().Get("lines"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				if n > 1000 {
					n = 1000
				}
				lines = n
			}
		}
		content, err := s.sv.ReadLog(botID, lines)
		if err != nil {
			writeJSON(w, map[string]interface{}{"error": err.Error(), "logs": ""})
			return
		}
		writeJSON(w, map[string]interface{}{"bot": botID, "lines": lines, "logs": content})
		return
	}

	// GET /api/bots/:id/status
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "status" {
		var botCfg *config.BotConfig
		if s.cfg != nil {
			for i := range s.cfg.Bots {
				if s.cfg.Bots[i].ID == botID {
					botCfg = &s.cfg.Bots[i]
					break
				}
			}
		}
		if botCfg == nil {
			http.Error(w, "bot not found", 404)
			return
		}

		result := map[string]interface{}{"bot": botID, "type": botCfg.Type}

		switch botCfg.Type {
		case "telegram":
			resp, err := s.httpClient.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", botCfg.Token))
			if err != nil {
				result["connected"] = false
				result["error"] = err.Error()
			} else {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				result["connected"] = resp.StatusCode == 200
				var tgResp map[string]interface{}
				json.Unmarshal(body, &tgResp)
				if r, ok := tgResp["result"].(map[string]interface{}); ok {
					result["bot_username"] = r["username"]
					result["bot_name"] = r["first_name"]
				}
			}
		default:
			result["connected"] = false
			result["error"] = "status check not implemented for " + botCfg.Type
		}

		writeJSON(w, result)
		return
	}

	// DELETE /api/bots/:id
	if r.Method == http.MethodDelete && len(parts) == 1 {
		s.sv.RemoveBot(botID)
		if s.cfg != nil {
			for i, b := range s.cfg.Bots {
				if b.ID == botID {
					s.cfg.Bots = append(s.cfg.Bots[:i], s.cfg.Bots[i+1:]...)
					break
				}
			}
			config.Save(s.configPath, s.cfg)
		}
		writeJSON(w, map[string]string{"status": "deleted", "bot": botID})
		return
	}

	// POST /api/bots/:id/toggle
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "toggle" {
		if s.cfg != nil {
			for i := range s.cfg.Bots {
				if s.cfg.Bots[i].ID == botID {
					s.cfg.Bots[i].Enabled = !s.cfg.Bots[i].Enabled
					config.Save(s.configPath, s.cfg)
					if s.cfg.Bots[i].Enabled {
						s.sv.RestartBot(botID)
					}
					writeJSON(w, map[string]interface{}{"status": "toggled", "enabled": s.cfg.Bots[i].Enabled})
					return
				}
			}
		}
		http.Error(w, "bot not found", 404)
		return
	}

	// GET/PUT/POST /api/bots/:id/access
	if len(parts) == 2 && parts[1] == "access" {
		s.handleBotAccess(w, r, botID)
		return
	}

	// POST /api/bots/:id/access/detect
	if r.Method == http.MethodPost && len(parts) == 3 && parts[1] == "access" && parts[2] == "detect" {
		s.handleBotAccessDetect(w, r, botID)
		return
	}

	http.Error(w, "not found", 404)
}

// --- Channel API ---

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	allChannels := s.sv.AllChannels()
	writeJSON(w, map[string]interface{}{"channels": allChannels})
}

// --- Version API ---

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	if s.versionMgr == nil {
		writeJSON(w, map[string]interface{}{
			"versions": []interface{}{},
			"default":  "system",
		})
		return
	}

	// Determine sub-path for install/activate
	subPath := strings.TrimPrefix(r.URL.Path, "/api/versions")
	subPath = strings.Trim(subPath, "/")

	if r.Method == http.MethodPost && subPath == "install" {
		var body struct {
			Version string `json:"version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Version == "" {
			http.Error(w, "version required", 400)
			return
		}
		if err := s.versionMgr.Install(body.Version); err != nil {
			writeJSON(w, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]interface{}{"status": "installed", "version": body.Version})
		return
	}

	if r.Method == http.MethodPost && subPath == "activate" {
		var body struct {
			Version string `json:"version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Version == "" {
			http.Error(w, "version required", 400)
			return
		}
		s.versionMgr.SetDefault(body.Version)
		writeJSON(w, map[string]interface{}{
			"status":  "activated",
			"version": body.Version,
			"note":    "in-memory only, restart will revert",
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"versions": s.versionMgr.List(),
		"default":  s.versionMgr.DefaultVersion(),
	})
}

// --- Config API ---

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, map[string]interface{}{"error": "config not available"})
		return
	}
	bots := make([]map[string]interface{}, len(s.cfg.Bots))
	for i, b := range s.cfg.Bots {
		token := "***"
		if len(b.Token) > 10 {
			token = b.Token[:5] + "..." + b.Token[len(b.Token)-4:]
		}
		bots[i] = map[string]interface{}{
			"id":                 b.ID,
			"type":               b.Type,
			"name":               b.Name,
			"enabled":            b.Enabled,
			"token":              token,
			"plugin":             b.Plugin,
			"plugin_marketplace": b.PluginMarketplace,
			"model":              b.Model,
			"claude_version":     b.ClaudeVersion,
		}
	}
	writeJSON(w, map[string]interface{}{
		"admin":      s.cfg.Admin,
		"supervisor": s.cfg.Supervisor,
		"claude":     s.cfg.Claude,
		"bots":       bots,
		"channels":   s.cfg.Channels,
	})
}

// --- Per-Bot Access Control API ---

// accessPathForBot returns the access.json path for a given bot type.
func accessPathForBot(botType string) string {
	switch botType {
	case "telegram":
		return filepath.Join(os.Getenv("HOME"), ".claude", "channels", "telegram", "access.json")
	case "discord":
		return filepath.Join(os.Getenv("HOME"), ".claude", "channels", "discord", "access.json")
	default:
		return filepath.Join(os.Getenv("HOME"), ".claude", "channels", botType, "access.json")
	}
}

func (s *Server) findBotConfig(botID string) *config.BotConfig {
	if s.cfg == nil {
		return nil
	}
	for i := range s.cfg.Bots {
		if s.cfg.Bots[i].ID == botID {
			return &s.cfg.Bots[i]
		}
	}
	return nil
}

func (s *Server) handleBotAccess(w http.ResponseWriter, r *http.Request, botID string) {
	botCfg := s.findBotConfig(botID)
	if botCfg == nil {
		http.Error(w, "bot not found", 404)
		return
	}
	accessPath := accessPathForBot(botCfg.Type)

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(accessPath)
		if err != nil {
			writeJSON(w, map[string]interface{}{"error": "access.json not found", "access": nil, "bot": botID})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), 400)
			return
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(body, &parsed); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), 400)
			return
		}
		os.MkdirAll(filepath.Dir(accessPath), 0700)
		formatted, _ := json.MarshalIndent(parsed, "", "  ")
		if err := os.WriteFile(accessPath, formatted, 0644); err != nil {
			http.Error(w, "write: "+err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "saved", "bot": botID})

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), 400)
			return
		}
		var req struct {
			Action  string `json:"action"`
			ID      string `json:"id"`
			Mention bool   `json:"require_mention"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), 400)
			return
		}

		data, _ := os.ReadFile(accessPath)
		var access map[string]interface{}
		if err := json.Unmarshal(data, &access); err != nil {
			access = map[string]interface{}{
				"dmPolicy": "allowlist", "allowFrom": []interface{}{},
				"groups": map[string]interface{}{}, "delivery": map[string]interface{}{},
			}
		}
		if access["groups"] == nil {
			access["groups"] = map[string]interface{}{}
		}
		if access["allowFrom"] == nil {
			access["allowFrom"] = []interface{}{}
		}

		groups, _ := access["groups"].(map[string]interface{})
		allowFrom, _ := access["allowFrom"].([]interface{})

		switch req.Action {
		case "add_group":
			groups[req.ID] = map[string]interface{}{"allowFrom": []interface{}{}, "requireMention": req.Mention}
			access["groups"] = groups
		case "remove_group":
			delete(groups, req.ID)
			access["groups"] = groups
		case "add_user":
			found := false
			for _, u := range allowFrom {
				if u == req.ID {
					found = true
					break
				}
			}
			if !found {
				access["allowFrom"] = append(allowFrom, req.ID)
			}
		case "remove_user":
			var filtered []interface{}
			for _, u := range allowFrom {
				if u != req.ID {
					filtered = append(filtered, u)
				}
			}
			access["allowFrom"] = filtered
		case "set_dm_policy":
			access["dmPolicy"] = req.ID // reuse ID field for policy value
		default:
			http.Error(w, "unknown action: "+req.Action, 400)
			return
		}

		formatted, _ := json.MarshalIndent(access, "", "  ")
		os.MkdirAll(filepath.Dir(accessPath), 0700)
		os.WriteFile(accessPath, formatted, 0644)
		writeJSON(w, map[string]interface{}{"status": "updated", "bot": botID, "access": access})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleBotAccessDetect(w http.ResponseWriter, r *http.Request, botID string) {
	botCfg := s.findBotConfig(botID)
	if botCfg == nil {
		http.Error(w, "bot not found", 404)
		return
	}
	if botCfg.Type != "telegram" {
		writeJSON(w, map[string]interface{}{"error": "detect only supported for telegram bots"})
		return
	}

	// Stop the bot so we can call getUpdates
	s.sv.RestartBot(botID)
	time.Sleep(2 * time.Second)

	resp, err := s.httpClient.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?limit=100&timeout=2", botCfg.Token))
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": "getUpdates failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var tgResp struct {
		OK     bool `json:"ok"`
		Result []struct {
			Message *struct {
				Chat struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
					Type  string `json:"type"`
				} `json:"chat"`
				From struct {
					ID       int64  `json:"id"`
					Username string `json:"username"`
					Name     string `json:"first_name"`
				} `json:"from"`
			} `json:"message"`
			MyChatMember *struct {
				Chat struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
					Type  string `json:"type"`
				} `json:"chat"`
			} `json:"my_chat_member"`
		} `json:"result"`
	}
	json.Unmarshal(body, &tgResp)

	// Load current access
	accessPath := accessPathForBot(botCfg.Type)
	accessData, _ := os.ReadFile(accessPath)
	var access map[string]interface{}
	json.Unmarshal(accessData, &access)
	knownGroups := map[string]bool{}
	if g, ok := access["groups"].(map[string]interface{}); ok {
		for gid := range g {
			knownGroups[gid] = true
		}
	}
	knownUsers := map[string]bool{}
	if u, ok := access["allowFrom"].([]interface{}); ok {
		for _, uid := range u {
			knownUsers[fmt.Sprint(uid)] = true
		}
	}

	type detected struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Type  string `json:"type"`
		Known bool   `json:"known"`
	}
	seen := map[string]bool{}
	var detectedGroups []detected
	var detectedUsers []detected

	for _, u := range tgResp.Result {
		if u.Message != nil {
			chat := u.Message.Chat
			cid := fmt.Sprintf("%d", chat.ID)
			if !seen[cid] {
				seen[cid] = true
				if chat.Type == "group" || chat.Type == "supergroup" {
					detectedGroups = append(detectedGroups, detected{ID: cid, Title: chat.Title, Type: chat.Type, Known: knownGroups[cid]})
				} else if chat.Type == "private" {
					from := u.Message.From
					uid := fmt.Sprintf("%d", from.ID)
					name := from.Name
					if from.Username != "" {
						name = "@" + from.Username
					}
					detectedUsers = append(detectedUsers, detected{ID: uid, Title: name, Type: "private", Known: knownUsers[uid]})
				}
			}
		}
		if u.MyChatMember != nil {
			chat := u.MyChatMember.Chat
			cid := fmt.Sprintf("%d", chat.ID)
			if !seen[cid] && (chat.Type == "group" || chat.Type == "supergroup") {
				seen[cid] = true
				detectedGroups = append(detectedGroups, detected{ID: cid, Title: chat.Title, Type: chat.Type, Known: knownGroups[cid]})
			}
		}
	}

	writeJSON(w, map[string]interface{}{
		"bot":    botID,
		"groups": detectedGroups,
		"users":  detectedUsers,
		"note":   "봇이 자동 재시작됩니다",
	})
}

// --- System API ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	bots := s.sv.Status()
	running := 0
	failed := 0
	for _, b := range bots {
		if b.State == "running" {
			running++
		}
		if b.State == "failed" {
			failed++
		}
	}
	status := "healthy"
	if failed > 0 {
		status = "degraded"
	}
	if running == 0 && len(bots) > 0 {
		status = "down"
	}
	writeJSON(w, map[string]interface{}{
		"status":       status,
		"total_bots":   len(bots),
		"running_bots": running,
		"failed_bots":  failed,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{"events": s.sv.GetEvents(50)})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	bots := s.sv.Status()
	healthy := true
	for _, b := range bots {
		if b.State == "failed" {
			healthy = false
		}
	}
	code := 200
	if !healthy {
		code = 503
	}
	w.WriteHeader(code)
	writeJSON(w, map[string]interface{}{"healthy": healthy, "bots": len(bots)})
}

// --- Conversation API ---

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, map[string]interface{}{"channels": []interface{}{}})
		return
	}

	path := r.URL.Path[len("/api/conversations/"):]
	parts := splitPath(path)

	if len(parts) == 0 {
		channels, _ := s.store.ListChannels()
		var result []map[string]interface{}
		for _, ch := range channels {
			stats, _ := s.store.Stats(ch)
			result = append(result, map[string]interface{}{
				"channel_id":         ch,
				"conversation_count": stats.ConversationCount,
				"total_messages":     stats.TotalMessages,
			})
		}
		writeJSON(w, map[string]interface{}{"channels": result})
		return
	}

	channelID := parts[0]

	if len(parts) == 1 {
		summaries, err := s.store.ListByChannel(channelID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		stats, _ := s.store.Stats(channelID)
		writeJSON(w, map[string]interface{}{
			"channel_id":    channelID,
			"stats":         stats,
			"conversations": summaries,
		})
		return
	}

	convID := parts[1]

	if r.Method == http.MethodDelete {
		if err := s.store.Delete(channelID, convID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "deleted", "id": convID})
		return
	}

	conv, err := s.store.Load(channelID, convID)
	if err != nil || conv == nil {
		http.Error(w, "conversation not found", 404)
		return
	}
	writeJSON(w, conv)
}

// --- Dashboard ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

// --- Utilities ---

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range splitBy(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitBy(s string, sep byte) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Claude Channel Hub &#x2014; &#xB300;&#xC2DC;&#xBCF4;&#xB4DC;</title>
<style>
*, *::before, *::after { margin: 0; padding: 0; box-sizing: border-box; }

:root {
  --bg:        #0d1117;
  --surface:   #161b22;
  --surface2:  #1c2128;
  --border:    #30363d;
  --border2:   #21262d;
  --text:      #e6edf3;
  --text2:     #8b949e;
  --text3:     #6e7681;
  --green:     #3fb950;
  --green-bg:  rgba(63,185,80,.12);
  --yellow:    #d29922;
  --yellow-bg: rgba(210,153,34,.12);
  --red:       #f85149;
  --red-bg:    rgba(248,81,73,.12);
  --blue:      #58a6ff;
  --blue-bg:   rgba(88,166,255,.12);
  --radius:    8px;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans KR', sans-serif;
  background: var(--bg);
  color: var(--text);
  min-height: 100vh;
  font-size: 14px;
  line-height: 1.5;
}

.header {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  padding: 0 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  position: sticky;
  top: 0;
  z-index: 100;
}
.header-left { display: flex; align-items: center; gap: 10px; }
.header-title { font-size: 16px; font-weight: 700; letter-spacing: -.3px; }
.header-right { display: flex; align-items: center; gap: 12px; }
.refresh-info { font-size: 12px; color: var(--text3); }
#last-updated { color: var(--text2); }

.health-badge {
  display: inline-flex; align-items: center; gap: 6px;
  font-size: 12px; font-weight: 600;
  padding: 4px 12px; border-radius: 20px; border: 1px solid;
}
.health-badge.healthy  { color: var(--green);  background: var(--green-bg);  border-color: rgba(63,185,80,.3); }
.health-badge.degraded { color: var(--yellow); background: var(--yellow-bg); border-color: rgba(210,153,34,.3); }
.health-badge.down     { color: var(--red);    background: var(--red-bg);    border-color: rgba(248,81,73,.3); }
.health-badge .dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.health-badge.healthy .dot { animation: pulse 2s infinite; }

@keyframes pulse { 0%,100%{opacity:1} 50%{opacity:.35} }

.tabs {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  padding: 0 24px;
  display: flex;
}
.tab {
  padding: 12px 18px; cursor: pointer; color: var(--text2);
  border-bottom: 2px solid transparent; font-size: 13px; font-weight: 500;
  transition: color .15s, border-color .15s; user-select: none;
}
.tab:hover  { color: var(--text); }
.tab.active { color: var(--text); border-bottom-color: var(--blue); }

.container { max-width: 1140px; margin: 0 auto; padding: 28px 24px; }
.tab-pane { display: none; }
.tab-pane.active { display: block; }

.summary-row {
  display: grid; grid-template-columns: repeat(4, 1fr);
  gap: 12px; margin-bottom: 28px;
}
.summary-card {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 16px 20px;
  display: flex; flex-direction: column; gap: 4px;
}
.summary-card .s-val { font-size: 28px; font-weight: 700; line-height: 1; }
.summary-card .s-lbl { font-size: 12px; color: var(--text2); font-weight: 500; }
.summary-card.green  .s-val { color: var(--green); }
.summary-card.yellow .s-val { color: var(--yellow); }
.summary-card.red    .s-val { color: var(--red); }
.summary-card.blue   .s-val { color: var(--blue); }

.section-title {
  font-size: 11px; font-weight: 700; color: var(--text3);
  text-transform: uppercase; letter-spacing: .8px; margin: 0 0 12px;
}

.bot-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(320px,1fr));
  gap: 14px;
}
.bot-card {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); overflow: hidden; transition: border-color .15s;
}
.bot-card:hover { border-color: #444c56; }
.bot-card-header {
  padding: 14px 16px; display: flex; align-items: flex-start;
  justify-content: space-between; border-bottom: 1px solid var(--border2);
}
.bot-name-row { display: flex; flex-direction: column; gap: 3px; }
.bot-name { font-size: 15px; font-weight: 600; }
.bot-id   { font-size: 11px; color: var(--text3); font-family: monospace; }

.state-badge {
  font-size: 11px; font-weight: 600;
  padding: 3px 9px; border-radius: 20px; border: 1px solid;
  white-space: nowrap; flex-shrink: 0;
}
.state-badge.running { color: var(--green);  background: var(--green-bg);  border-color: rgba(63,185,80,.3); }
.state-badge.failed  { color: var(--red);    background: var(--red-bg);    border-color: rgba(248,81,73,.3); }
.state-badge.idle,
.state-badge.stopped { color: var(--text2);  background: var(--surface2);  border-color: var(--border); }

.bot-metrics {
  display: grid; grid-template-columns: repeat(3,1fr);
  border-bottom: 1px solid var(--border2);
}
.bot-metric {
  padding: 10px 14px; text-align: center;
  border-right: 1px solid var(--border2);
}
.bot-metric:last-child { border-right: none; }
.bot-metric .m-val { font-size: 18px; font-weight: 700; }
.bot-metric .m-lbl { font-size: 10px; color: var(--text2); margin-top: 1px; }

.bot-footer {
  padding: 10px 16px; display: flex;
  align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 6px;
}
.bot-actions { display: flex; gap: 6px; flex-wrap: wrap; }

.btn {
  font-size: 11px; font-weight: 600; padding: 4px 10px;
  border-radius: 5px; border: 1px solid; cursor: pointer;
  transition: opacity .15s; background: transparent;
}
.btn:hover { opacity: .75; }
.btn-restart  { color: var(--yellow); border-color: rgba(210,153,34,.4); }
.btn-logs     { color: var(--blue);   border-color: rgba(88,166,255,.4); }
.btn-status   { color: var(--green);  border-color: rgba(63,185,80,.4); }
.btn-activate { color: var(--blue);   border-color: rgba(88,166,255,.4); }
.btn-sm { font-size: 11px; padding: 3px 8px; }

.bot-type {
  font-size: 11px; padding: 2px 8px; border-radius: 4px;
  background: var(--surface2); color: var(--text2);
  border: 1px solid var(--border); font-family: monospace;
}

.channel-list { padding: 0 16px 10px; display: flex; flex-wrap: wrap; gap: 6px; }
.ch-chip {
  font-size: 11px; padding: 2px 8px; border-radius: 4px;
  background: var(--blue-bg); color: var(--blue);
  border: 1px solid rgba(88,166,255,.2); font-family: monospace;
}

.events-card {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); overflow: hidden;
}
.events-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.events-table th {
  text-align: left; padding: 10px 14px; color: var(--text2);
  border-bottom: 1px solid var(--border); font-weight: 600;
  font-size: 12px; background: var(--surface2);
}
.events-table td {
  padding: 8px 14px; border-bottom: 1px solid var(--border2);
  vertical-align: middle;
}
.events-table tr:last-child td { border-bottom: none; }
.events-table tr:hover td { background: var(--surface2); }
.time-cell { color: var(--text3); white-space: nowrap; font-size: 12px; font-family: monospace; }
.id-cell   { color: var(--text2); font-family: monospace; font-size: 12px; }

.ev-tag {
  display: inline-block; font-size: 11px; font-weight: 600;
  padding: 2px 8px; border-radius: 4px; text-transform: lowercase;
}
.ev-tag.started   { color: var(--green);  background: var(--green-bg); }
.ev-tag.stopped   { color: var(--yellow); background: var(--yellow-bg); }
.ev-tag.failed,
.ev-tag.error     { color: var(--red);    background: var(--red-bg); }
.ev-tag.restarted,
.ev-tag.restart_requested { color: var(--blue); background: var(--blue-bg); }
.ev-tag.message   { color: var(--text2);  background: var(--surface2); }

.empty-state { padding: 48px; text-align: center; color: var(--text3); font-size: 13px; }

/* Versions tab */
.versions-grid {
  display: grid; grid-template-columns: repeat(auto-fill,minmax(280px,1fr));
  gap: 12px; margin-bottom: 20px;
}
.version-card {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 16px;
}
.version-card.active-ver { border-color: rgba(63,185,80,.5); }
.version-tag {
  font-family: monospace; font-size: 14px; font-weight: 700;
  display: flex; align-items: center; gap: 8px; margin-bottom: 8px;
}
.active-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--green); flex-shrink: 0; }
.version-meta { font-size: 12px; color: var(--text3); margin-bottom: 10px; font-family: monospace; word-break: break-all; }

.install-form {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 16px;
  display: flex; gap: 10px; align-items: flex-end; flex-wrap: wrap;
}
.form-group { display: flex; flex-direction: column; gap: 4px; }
.form-label { font-size: 12px; color: var(--text2); font-weight: 500; }
.form-input {
  background: var(--bg); border: 1px solid var(--border);
  color: var(--text); padding: 6px 10px; border-radius: 5px;
  font-size: 13px; width: 180px;
}
.form-input:focus { outline: none; border-color: var(--blue); }
.btn-primary {
  background: var(--blue); color: #fff; border: none;
  padding: 7px 14px; border-radius: 5px; font-size: 13px;
  font-weight: 600; cursor: pointer;
}
.btn-primary:hover { opacity: .85; }

/* Config tab */
.config-section {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); margin-bottom: 14px; overflow: hidden;
}
.config-section-header {
  padding: 12px 16px; background: var(--surface2);
  border-bottom: 1px solid var(--border);
  display: flex; justify-content: space-between; align-items: center;
  cursor: pointer; user-select: none;
}
.config-section-title { font-size: 13px; font-weight: 600; }
.config-toggle { font-size: 11px; color: var(--text3); }
.config-section-body.collapsed { display: none; }
.config-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.config-table td { padding: 8px 16px; border-bottom: 1px solid var(--border2); }
.config-table tr:last-child td { border-bottom: none; }
.config-key { color: var(--text2); width: 40%; font-family: monospace; font-size: 12px; }
.config-val { font-family: monospace; font-size: 12px; word-break: break-all; }
.config-val.masked { color: var(--text3); }

/* Troubleshoot tab */
.ts-select-row {
  display: flex; align-items: center; gap: 12px; margin-bottom: 20px; flex-wrap: wrap;
}
.ts-select {
  background: var(--surface); border: 1px solid var(--border);
  color: var(--text); padding: 7px 12px; border-radius: 5px; font-size: 13px;
}
.ts-auto-label {
  font-size: 12px; color: var(--text2);
  display: flex; align-items: center; gap: 6px; cursor: pointer;
}
.ts-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.ts-card {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); overflow: hidden;
}
.ts-card-header {
  padding: 10px 14px; background: var(--surface2);
  border-bottom: 1px solid var(--border);
  font-size: 12px; font-weight: 600; color: var(--text2);
}
.ts-card-body { padding: 14px; }
.log-pre {
  font-family: monospace; font-size: 11px; color: var(--text2);
  white-space: pre-wrap; word-break: break-all;
  max-height: 300px; overflow-y: auto;
  background: var(--bg); padding: 10px; border-radius: 5px;
  border: 1px solid var(--border2);
}
.conn-status { font-size: 13px; display: flex; align-items: center; gap: 8px; }
.conn-dot { width: 10px; height: 10px; border-radius: 50%; background: var(--text3); flex-shrink: 0; }
.conn-dot.ok  { background: var(--green); }
.conn-dot.err { background: var(--red); }

/* Modal */
.modal-overlay {
  display: none; position: fixed; inset: 0;
  background: rgba(0,0,0,.65); z-index: 200;
  align-items: center; justify-content: center;
}
.modal-overlay.open { display: flex; }
.modal {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--radius); width: 90vw; max-width: 860px;
  max-height: 85vh; display: flex; flex-direction: column;
}
.modal-header {
  padding: 14px 18px; border-bottom: 1px solid var(--border);
  display: flex; align-items: center; justify-content: space-between;
}
.modal-title { font-size: 14px; font-weight: 600; }
.modal-close {
  background: none; border: none; color: var(--text2);
  font-size: 18px; cursor: pointer; padding: 2px 6px;
}
.modal-close:hover { color: var(--text); }
.modal-body { padding: 16px; overflow-y: auto; flex: 1; }
.modal-log {
  font-family: monospace; font-size: 12px; color: var(--text2);
  white-space: pre-wrap; word-break: break-all;
  background: var(--bg); padding: 12px; border-radius: 5px;
  border: 1px solid var(--border2); min-height: 200px;
}

@media (max-width: 700px) {
  .summary-row { grid-template-columns: repeat(2,1fr); }
  .bot-grid    { grid-template-columns: 1fr; }
  .container   { padding: 16px; }
  .ts-grid     { grid-template-columns: 1fr; }
}
</style>
</head>
<body>

<header class="header">
  <div class="header-left">
    <div class="header-title">&#x1F916; Claude Channel Hub</div>
  </div>
  <div class="header-right">
    <span class="refresh-info">&#xB9C8;&#xC9C0;&#xB9C9; &#xAC31;&#xC2E0;: <span id="last-updated">&#x2014;</span></span>
    <span class="health-badge healthy" id="health-badge">
      <span class="dot"></span><span id="health-text">&#xC815;&#xC0C1;</span>
    </span>
  </div>
</header>

<nav class="tabs">
  <div class="tab active" data-tab="bots">&#xBD07; &#xBAA9;&#xB85D;</div>
  <div class="tab" data-tab="events">&#xC774;&#xBCA4;&#xD2B8; &#xB85C;&#xADF8;</div>
  <div class="tab" data-tab="versions">&#xBC84;&#xC804; &#xAD00;&#xB9AC;</div>
  <div class="tab" data-tab="config">&#xC124;&#xC815;</div>
  <div class="tab" data-tab="troubleshoot">&#xD2B8;&#xB7EC;&#xBE14;&#xC288;&#xD305;</div>
</nav>

<main class="container">

  <!-- Bots tab -->
  <div class="tab-pane active" id="pane-bots">
    <div class="summary-row">
      <div class="summary-card blue">
        <div class="s-val" id="sum-total">&#x2014;</div>
        <div class="s-lbl">&#xC804;&#xCCB4; &#xBD07;</div>
      </div>
      <div class="summary-card green">
        <div class="s-val" id="sum-running">&#x2014;</div>
        <div class="s-lbl">&#xC2E4;&#xD589; &#xC911;</div>
      </div>
      <div class="summary-card red">
        <div class="s-val" id="sum-failed">&#x2014;</div>
        <div class="s-lbl">&#xC624;&#xB958;</div>
      </div>
      <div class="summary-card yellow">
        <div class="s-val" id="sum-channels">&#x2014;</div>
        <div class="s-lbl">&#xC804;&#xCCB4; &#xCC44;&#xB110;</div>
      </div>
    </div>
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
      <div class="section-title" style="margin:0">&#xBD07; &#xC0C1;&#xD0DC;</div>
      <button onclick="showAddBotModal()" style="padding:6px 14px;background:#238636;border:none;border-radius:6px;color:#fff;cursor:pointer;font-size:13px;font-weight:600">+ &#xBD07; &#xCD94;&#xAC00;</button>
    </div>
    <div class="bot-grid" id="bot-grid">
      <div class="empty-state">&#xB370;&#xC774;&#xD130; &#xB85C;&#xB529; &#xC911;&#x2026;</div>
    </div>
  </div>

  <!-- Events tab -->
  <div class="tab-pane" id="pane-events">
    <div class="section-title">&#xCD5C;&#xADFC; &#xC774;&#xBCA4;&#xD2B8; (&#xCD5C;&#xB300; 50&#xAC1C;)</div>
    <div class="events-card">
      <table class="events-table">
        <thead>
          <tr>
            <th style="width:90px">&#xC2DC;&#xAC04;</th>
            <th style="width:110px">&#xACBD;&#xACFC;</th>
            <th style="width:160px">&#xBD07; / &#xCC44;&#xB110;</th>
            <th style="width:130px">&#xC561;&#xC158;</th>
            <th>&#xC0C1;&#xC138;</th>
          </tr>
        </thead>
        <tbody id="events-body">
          <tr><td colspan="5" class="empty-state">&#xC774;&#xBCA4;&#xD2B8; &#xC5C6;&#xC74C;</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- Versions tab -->
  <div class="tab-pane" id="pane-versions">
    <div class="section-title">Claude Code &#xBC84;&#xC804;</div>
    <div class="versions-grid" id="versions-grid">
      <div class="empty-state">&#xB85C;&#xB529; &#xC911;&#x2026;</div>
    </div>
    <div class="install-form">
      <div class="form-group">
        <label class="form-label" for="install-ver-input">&#xBC84;&#xC804; &#xBC88;&#xD638; (&#xC608;: 2.1.104)</label>
        <input class="form-input" id="install-ver-input" type="text" placeholder="2.1.104">
      </div>
      <button class="btn-primary" onclick="installVersion()">&#xC124;&#xCE58;</button>
    </div>
  </div>

  <!-- Config tab -->
  <div class="tab-pane" id="pane-config">
    <div class="section-title">&#xD604;&#xC7AC; &#xC124;&#xC815; (&#xC77D;&#xAE30; &#xC804;&#xC6A9;)</div>
    <div id="config-content">
      <div class="empty-state">&#xB85C;&#xB529; &#xC911;&#x2026;</div>
    </div>
  </div>

  <!-- Troubleshoot tab -->
  <div class="tab-pane" id="pane-troubleshoot">
    <div class="ts-select-row">
      <select class="ts-select" id="ts-bot-select" onchange="loadTroubleshoot()">
        <option value="">&#xBD07; &#xC120;&#xD0DD;&#x2026;</option>
      </select>
      <label class="ts-auto-label">
        <input type="checkbox" id="ts-auto" checked onchange="toggleTsAuto()">
        5&#xCD08; &#xC790;&#xB3D9; &#xAC31;&#xC2E0;
      </label>
      <button class="btn btn-logs btn-sm" onclick="loadTroubleshoot()">&#xC218;&#xB3D9; &#xAC31;&#xC2E0;</button>
    </div>
    <div class="ts-grid" id="ts-grid">
      <div class="empty-state" style="grid-column:1/-1">&#xBD07;&#xC744; &#xC120;&#xD0DD;&#xD558;&#xC138;&#xC694;.</div>
    </div>
  </div>

</main>

<!-- Add Bot Modal -->
<div id="addBotModal" style="display:none;position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.7);z-index:1000;justify-content:center;align-items:center">
  <div style="background:#161b22;border:1px solid #30363d;border-radius:12px;padding:24px;max-width:520px;width:90%;max-height:80vh;overflow-y:auto">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px">
      <h3 style="margin:0;font-size:16px">&#xBD07; &#xCD94;&#xAC00;</h3>
      <button onclick="closeAddBotModal()" style="background:none;border:none;color:#8b949e;cursor:pointer;font-size:18px">&#x2715;</button>
    </div>
    <div style="display:flex;flex-direction:column;gap:14px">
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">ID <span style="color:#f85149">*</span></label>
        <input id="add-bot-id" placeholder="my-bot" style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px">
      </div>
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">&#xD0C0;&#xC785;</label>
        <select id="add-bot-type" style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px">
          <option value="telegram">telegram</option>
          <option value="discord">discord</option>
        </select>
      </div>
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">&#xC774;&#xB984;</label>
        <input id="add-bot-name" placeholder="My Bot" style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px">
      </div>
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">&#xD1A0;&#xD070; <span style="color:#f85149">*</span></label>
        <input id="add-bot-token" placeholder="1234567890:AAF..." type="password" style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px">
      </div>
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">&#xBAA8;&#xB378; (&#xC120;&#xD0DD;)</label>
        <input id="add-bot-model" placeholder="claude-opus-4-5" style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px">
      </div>
      <div>
        <label style="display:block;font-size:12px;color:#8b949e;margin-bottom:4px">&#xC2DC;&#xC2A4;&#xD15C; &#xD504;&#xB86C;&#xD504;&#xD2B8; (&#xC120;&#xD0DD;)</label>
        <textarea id="add-bot-system-prompt" rows="3" placeholder="You are a helpful assistant..." style="width:100%;padding:7px 10px;background:#0d1117;border:1px solid #30363d;border-radius:5px;color:#e6edf3;font-size:13px;resize:vertical"></textarea>
      </div>
    </div>
    <div style="display:flex;justify-content:flex-end;gap:10px;margin-top:20px">
      <button onclick="closeAddBotModal()" style="padding:7px 16px;background:transparent;border:1px solid #30363d;border-radius:6px;color:#e6edf3;cursor:pointer;font-size:13px">&#xCDE8;&#xC18C;</button>
      <button onclick="submitAddBot()" style="padding:7px 16px;background:#238636;border:none;border-radius:6px;color:#fff;cursor:pointer;font-size:13px;font-weight:600">&#xCD94;&#xAC00;</button>
    </div>
  </div>
</div>

<!-- Access Modal -->
<div id="accessModal" style="display:none;position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.7);z-index:1000;justify-content:center;align-items:center">
  <div style="background:#161b22;border:1px solid #30363d;border-radius:12px;padding:24px;max-width:700px;width:90%;max-height:80vh;overflow-y:auto">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
      <h3 id="accessModalTitle" style="margin:0;font-size:16px"></h3>
      <button onclick="closeAccessModal()" style="background:none;border:none;color:#8b949e;cursor:pointer;font-size:18px">&#x2715;</button>
    </div>

    <div style="display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px">
      <div style="background:#0d1117;border:1px solid #21262d;border-radius:8px;padding:12px">
        <h4 style="margin:0 0 8px;font-size:13px">&#xD5C8;&#xC6A9; &#xC0AC;&#xC6A9;&#xC790; (DM)</h4>
        <div id="modal-access-users"></div>
        <div style="display:flex;gap:8px;margin-top:8px">
          <input id="modal-new-user-id" placeholder="User ID" style="flex:1;padding:5px 8px;background:#161b22;border:1px solid #30363d;border-radius:4px;color:#e6edf3;font-size:12px">
          <button onclick="addUser()" style="padding:4px 10px;background:#238636;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:12px">&#xCD94;&#xAC00;</button>
        </div>
      </div>
      <div style="background:#0d1117;border:1px solid #21262d;border-radius:8px;padding:12px">
        <h4 style="margin:0 0 8px;font-size:13px">&#xD5C8;&#xC6A9; &#xADF8;&#xB8F9;</h4>
        <div id="modal-access-groups"></div>
        <div style="display:flex;gap:8px;margin-top:8px">
          <input id="modal-new-group-id" placeholder="Chat ID (-100...)" style="flex:1;padding:5px 8px;background:#161b22;border:1px solid #30363d;border-radius:4px;color:#e6edf3;font-size:12px">
          <label style="display:flex;align-items:center;gap:4px;font-size:11px;color:#8b949e;white-space:nowrap"><input type="checkbox" id="modal-new-group-mention"> &#xBA58;&#xC158;</label>
          <button onclick="addGroup()" style="padding:4px 10px;background:#238636;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:12px">&#xCD94;&#xAC00;</button>
        </div>
      </div>
    </div>

    <div style="display:flex;gap:12px;align-items:center;margin-bottom:16px">
      <span style="font-size:13px">DM &#xC815;&#xCC45;:</span>
      <select id="modal-dm-policy" onchange="updateDmPolicy()" style="padding:4px 8px;background:#0d1117;border:1px solid #30363d;border-radius:4px;color:#e6edf3;font-size:12px">
        <option value="allowlist">allowlist</option>
        <option value="pairing">pairing</option>
        <option value="disabled">disabled</option>
      </select>
      <span id="modal-dm-policy-status" style="font-size:11px;color:#8b949e"></span>
      <div style="flex:1"></div>
      <button onclick="detectGroups()" style="padding:6px 12px;background:#1f6feb;border:none;border-radius:6px;color:#fff;cursor:pointer;font-size:12px">&#xADF8;&#xB8F9;/&#xC0AC;&#xC6A9;&#xC790; &#xAC10;&#xC9C0;</button>
    </div>

    <div id="modal-detect-results"></div>
  </div>
</div>

<!-- Log Modal -->
<div class="modal-overlay" id="logModal">
  <div class="modal">
    <div class="modal-header">
      <div class="modal-title" id="logTitle">&#xB85C;&#xADF8;</div>
      <button class="modal-close" onclick="closeModal()">&#x2715;</button>
    </div>
    <div class="modal-body">
      <pre class="modal-log" id="logContent">&#xB85C;&#xB529; &#xC911;&#x2026;</pre>
    </div>
  </div>
</div>

<script>
(function () {
'use strict';

// Tab switching
document.querySelectorAll('.tab').forEach(function(tab) {
  tab.addEventListener('click', function() {
    var name = tab.dataset.tab;
    document.querySelectorAll('.tab').forEach(function(t) {
      t.classList.toggle('active', t.dataset.tab === name);
    });
    document.querySelectorAll('.tab-pane').forEach(function(p) {
      p.classList.toggle('active', p.id === 'pane-' + name);
    });
    if (name === 'events')       loadEvents();
    if (name === 'versions')     loadVersions();
    if (name === 'config')       loadConfig();
    if (name === 'troubleshoot') initTroubleshoot();
    // 'access' tab removed — access is now per-bot modal
  });
});

// Helpers
function esc(s) {
  var d = document.createElement('div');
  d.textContent = String(s == null ? '' : s);
  return d.innerHTML;
}

function relTime(isoStr) {
  if (!isoStr) return '';
  var diff = (Date.now() - new Date(isoStr).getTime()) / 1000;
  if (diff < 5)    return '\uBC29\uAE08';
  if (diff < 60)   return Math.floor(diff) + '\uCD08 \uC804';
  if (diff < 3600) return Math.floor(diff / 60) + '\uBD84 \uC804';
  if (diff < 86400) return Math.floor(diff / 3600) + '\uC2DC\uAC04 \uC804';
  return Math.floor(diff / 86400) + '\uC77C \uC804';
}

function timeStr(isoStr) {
  if (!isoStr) return '';
  try { return new Date(isoStr).toLocaleTimeString('ko-KR'); } catch(e) { return isoStr; }
}

function stateClass(state) {
  if (state === 'running') return 'running';
  if (state === 'failed')  return 'failed';
  return 'stopped';
}

function stateLabel(state) {
  var map = {
    running: '\uC2E4\uD589 \uC911',
    failed:  '\uC624\uB958',
    idle:    '\uC720\uD734',
    stopped: '\uC911\uC9C0\uB428'
  };
  return map[state] || state || '\u2014';
}

// Bots
function renderBots(bots) {
  var grid = document.getElementById('bot-grid');
  if (!bots || !bots.length) {
    grid.innerHTML = '<div class="empty-state">\uBD07\uC774 \uC5C6\uC2B5\uB2C8\uB2E4.</div>';
    return;
  }
  var totalChannels = 0, running = 0, failed = 0;
  bots.forEach(function(b) {
    if (b.state === 'running') running++;
    if (b.state === 'failed')  failed++;
    totalChannels += (b.channel_count || 0);
  });
  document.getElementById('sum-total').textContent    = bots.length;
  document.getElementById('sum-running').textContent  = running;
  document.getElementById('sum-failed').textContent   = failed;
  document.getElementById('sum-channels').textContent = totalChannels;

  grid.innerHTML = bots.map(function(b) {
    var cls   = stateClass(b.state);
    var label = stateLabel(b.state);
    var name  = esc(b.name || b.id || '(unnamed)');
    var id    = b.name ? esc(b.id) : '';
    var bid   = esc(b.id);
    var channels = (b.channels || []).map(function(ch) {
      return '<span class="ch-chip">' + esc(ch.id || ch) + '</span>';
    }).join('');
    var channelSection = channels ? '<div class="channel-list">' + channels + '</div>' : '';

    return (
      '<div class="bot-card">' +
        '<div class="bot-card-header">' +
          '<div class="bot-name-row">' +
            '<div class="bot-name">' + name + '</div>' +
            (id ? '<div class="bot-id">' + id + '</div>' : '') +
          '</div>' +
          '<span class="state-badge ' + cls + '">' + label + '</span>' +
        '</div>' +
        '<div class="bot-metrics">' +
          '<div class="bot-metric"><div class="m-val">' + esc(b.uptime || '\u2014') + '</div><div class="m-lbl">\uC5C5\uD0C0\uC784</div></div>' +
          '<div class="bot-metric"><div class="m-val">' + esc(b.restart_count != null ? b.restart_count : '\u2014') + '</div><div class="m-lbl">\uC7AC\uC2DC\uC791</div></div>' +
          '<div class="bot-metric"><div class="m-val">' + esc(b.channel_count != null ? b.channel_count : '\u2014') + '</div><div class="m-lbl">\uCC44\uB110</div></div>' +
        '</div>' +
        channelSection +
        '<div class="bot-footer">' +
          '<span class="bot-type">' + esc(b.type || 'unknown') + '</span>' +
          '<div class="bot-actions">' +
            '<button class="btn btn-restart" onclick="restartBot(\'' + bid + '\')">\uC7AC\uC2DC\uC791</button>' +
            '<button class="btn btn-logs"    onclick="showLogs(\'' + bid + '\')">\uB85C\uADF8 \uBCF4\uAE30</button>' +
            '<button class="btn btn-status"  onclick="checkStatus(\'' + bid + '\')">\uC0C1\uD0DC \uD655\uC778</button>' +
            '<button class="btn btn-logs"    onclick="showAccessModal(\'' + bid + '\')">\uC811\uADFC \uAD00\uB9AC</button>' +
            '<button class="btn" style="color:#f85149;border-color:rgba(248,81,73,.4)" onclick="deleteBot(\'' + bid + '\')">\uC0AD\uC81C</button>' +
          '</div>' +
        '</div>' +
      '</div>'
    );
  }).join('');
}

// Events
function renderEvents(events) {
  var tbody = document.getElementById('events-body');
  var list  = (events || []).slice().reverse().slice(0, 50);
  if (!list.length) {
    tbody.innerHTML = '<tr><td colspan="5" class="empty-state">\uC774\uBCA4\uD2B8 \uC5C6\uC74C</td></tr>';
    return;
  }
  tbody.innerHTML = list.map(function(ev) {
    var action = (ev.action || 'message').toLowerCase();
    var tagCls = ['started','stopped','failed','error','restarted','restart_requested'].indexOf(action) >= 0 ? action : 'message';
    return (
      '<tr>' +
        '<td class="time-cell">' + esc(timeStr(ev.time)) + '</td>' +
        '<td class="time-cell">' + esc(relTime(ev.time)) + '</td>' +
        '<td class="id-cell">'  + esc(ev.bot_id || ev.channel_id || '\u2014') + '</td>' +
        '<td><span class="ev-tag ' + tagCls + '">' + esc(action) + '</span></td>' +
        '<td>' + esc(ev.detail || '') + '</td>' +
      '</tr>'
    );
  }).join('');
}

// Versions
function renderVersions(data) {
  var grid = document.getElementById('versions-grid');
  var list = data.versions || [];
  if (!list.length) {
    grid.innerHTML = '<div class="empty-state">\uC124\uCE58\uB41C \uBC84\uC804 \uC5C6\uC74C</div>';
    return;
  }
  grid.innerHTML = list.map(function(v) {
    var isActive = v.active;
    var cardCls  = isActive ? 'version-card active-ver' : 'version-card';
    var activateBtn = isActive ? '' : '<button class="btn btn-activate btn-sm" onclick="activateVersion(\'' + esc(v.version) + '\')">\uD65C\uC131\uD654</button>';
    return (
      '<div class="' + cardCls + '">' +
        '<div class="version-tag">' +
          (isActive ? '<span class="active-dot"></span>' : '') +
          esc(v.version) +
          (v.system ? ' <span style="font-size:10px;color:var(--text3)">(system)</span>' : '') +
        '</div>' +
        '<div class="version-meta">' + esc(v.path || '') + '</div>' +
        activateBtn +
      '</div>'
    );
  }).join('');
}

// Config
function renderConfig(data) {
  var el = document.getElementById('config-content');
  var sections = [
    { key: 'admin',      label: 'Admin' },
    { key: 'supervisor', label: 'Supervisor' },
    { key: 'claude',     label: 'Claude' },
    { key: 'bots',       label: '\uBD07 (\uD1A0\uD070 \uB9C8\uC2A4\uD0B9)' },
    { key: 'channels',   label: '\uCC44\uB110' },
  ];
  el.innerHTML = sections.map(function(sec) {
    var val = data[sec.key];
    var rows = '';
    if (Array.isArray(val)) {
      val.forEach(function(item) {
        Object.keys(item).forEach(function(k) {
          var isMasked = (k === 'token');
          rows += '<tr><td class="config-key">' + esc((item.id || '') + '.' + k) + '</td>' +
                  '<td class="config-val' + (isMasked ? ' masked' : '') + '">' +
                  esc(item[k] != null ? String(item[k]) : '') + '</td></tr>';
        });
      });
    } else if (val && typeof val === 'object') {
      Object.keys(val).forEach(function(k) {
        rows += '<tr><td class="config-key">' + esc(k) + '</td>' +
                '<td class="config-val">' + esc(val[k] != null ? String(val[k]) : '') + '</td></tr>';
      });
    }
    return (
      '<div class="config-section">' +
        '<div class="config-section-header" onclick="toggleSection(this)">' +
          '<span class="config-section-title">' + esc(sec.label) + '</span>' +
          '<span class="config-toggle">\u25BC</span>' +
        '</div>' +
        '<div class="config-section-body">' +
          '<table class="config-table"><tbody>' + rows + '</tbody></table>' +
        '</div>' +
      '</div>'
    );
  }).join('');
}

window.toggleSection = function(header) {
  var body = header.nextElementSibling;
  body.classList.toggle('collapsed');
  var icon = header.querySelector('.config-toggle');
  icon.textContent = body.classList.contains('collapsed') ? '\u25BA' : '\u25BC';
};

// Troubleshoot
function initTroubleshoot() {
  fetch('/api/bots')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var sel  = document.getElementById('ts-bot-select');
      var bots = data.bots || [];
      sel.innerHTML = '<option value="">\uBD07 \uC120\uD0DD\u2026</option>' +
        bots.map(function(b) {
          return '<option value="' + esc(b.id) + '">' + esc(b.name || b.id) + '</option>';
        }).join('');
    });
}

window.loadTroubleshoot = function() {
  var botId = document.getElementById('ts-bot-select').value;
  if (!botId) return;
  var grid = document.getElementById('ts-grid');
  grid.innerHTML = '<div class="empty-state" style="grid-column:1/-1">\uB85C\uB529 \uC911\u2026</div>';

  Promise.all([
    fetch('/api/bots/' + encodeURIComponent(botId) + '/status').then(function(r) { return r.json(); }),
    fetch('/api/bots/' + encodeURIComponent(botId) + '/logs?lines=50').then(function(r) { return r.json(); }),
    fetch('/api/events').then(function(r) { return r.json(); }),
  ]).then(function(results) {
    var statusData = results[0];
    var logData    = results[1];
    var eventsData = results[2];

    var dotCls   = statusData.connected ? 'conn-dot ok' : 'conn-dot err';
    var connText = statusData.connected
      ? ('\u2705 \uC5F0\uACB0\uB428' + (statusData.bot_username ? ' (@' + esc(statusData.bot_username) + ')' : ''))
      : ('\u274C \uC5F0\uACB0 \uC2E4\uD328' + (statusData.error ? ': ' + esc(statusData.error) : ''));
    var connHtml = '<div class="conn-status"><span class="' + dotCls + '"></span>' + connText + '</div>';

    var logText = logData.logs || logData.error || '(\uB85C\uADF8 \uC5C6\uC74C)';

    var errEvents = (eventsData.events || []).filter(function(e) {
      return e.bot_id === botId && (e.action === 'error' || e.action === 'failed');
    }).slice(-10).reverse();
    var errHtml = errEvents.length
      ? errEvents.map(function(e) {
          return '<div style="font-size:12px;padding:4px 0;border-bottom:1px solid var(--border2)">' +
                 '<span style="color:var(--text3);font-family:monospace">' + esc(timeStr(e.time)) + '</span> ' +
                 esc(e.detail || '') + '</div>';
        }).join('')
      : '<div style="color:var(--text3);font-size:12px">\uCD5C\uADFC \uC624\uB958 \uC5C6\uC74C</div>';

    grid.innerHTML =
      '<div class="ts-card">' +
        '<div class="ts-card-header">\uC5F0\uACB0 \uC0C1\uD0DC</div>' +
        '<div class="ts-card-body">' + connHtml + '</div>' +
      '</div>' +
      '<div class="ts-card">' +
        '<div class="ts-card-header">\uCD5C\uADFC \uC624\uB958 \uC774\uBCA4\uD2B8</div>' +
        '<div class="ts-card-body">' + errHtml + '</div>' +
      '</div>' +
      '<div class="ts-card" style="grid-column:1/-1">' +
        '<div class="ts-card-header">\uCD5C\uADFC \uB85C\uADF8 (50\uC904)</div>' +
        '<div class="ts-card-body"><pre class="log-pre">' + esc(logText) + '</pre></div>' +
      '</div>';
  }).catch(function(err) {
    grid.innerHTML = '<div class="empty-state" style="grid-column:1/-1">\uB85C\uB529 \uC2E4\uD328: ' + esc(String(err)) + '</div>';
  });
};

window.toggleTsAuto = function() {};

// Health badge
function updateHealth(status) {
  var badge = document.getElementById('health-badge');
  var text  = document.getElementById('health-text');
  var map   = {
    healthy:  ['healthy',  '\uC815\uC0C1'],
    degraded: ['degraded', '\uC77C\uBD80 \uC624\uB958'],
    down:     ['down',     '\uB2E4\uC6B4']
  };
  var info = map[status] || map['healthy'];
  badge.className = 'health-badge ' + info[0];
  text.textContent = info[1];
}

// Modal
function closeModal() {
  document.getElementById('logModal').classList.remove('open');
}
window.closeModal = closeModal;
document.getElementById('logModal').addEventListener('click', function(e) {
  if (e.target === this) closeModal();
});

// Actions
window.restartBot = function(id) {
  if (!confirm(id + ' \uBD07\uC744 \uC7AC\uC2DC\uC791\uD558\uC2DC\uACA0\uC2B5\uB2C8\uAE4C?')) return;
  fetch('/api/bots/' + encodeURIComponent(id) + '/restart', { method: 'POST' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      alert(data.status || data.error || '\uC694\uCCAD \uC644\uB8CC');
      loadBots();
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

window.showLogs = function(id) {
  document.getElementById('logTitle').textContent = id + ' \uB85C\uADF8';
  document.getElementById('logContent').textContent = '\uB85C\uB529 \uC911\u2026';
  document.getElementById('logModal').classList.add('open');
  fetch('/api/bots/' + encodeURIComponent(id) + '/logs?lines=200')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      document.getElementById('logContent').textContent = data.logs || data.error || '(\uB85C\uADF8 \uC5C6\uC74C)';
    })
    .catch(function(e) {
      document.getElementById('logContent').textContent = '\uC624\uB958: ' + e;
    });
};

window.checkStatus = function(id) {
  fetch('/api/bots/' + encodeURIComponent(id) + '/status')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.connected) {
        alert('\u2705 ' + (data.bot_username ? '@' + data.bot_username : id) + ' \uC5F0\uACB0\uB428');
      } else {
        alert('\u274C \uC5F0\uACB0 \uC2E4\uD328: ' + (data.error || '\uC54C \uC218 \uC5C6\uB294 \uC624\uB958'));
      }
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

window.installVersion = function() {
  var ver = document.getElementById('install-ver-input').value.trim();
  if (!ver) { alert('\uBC84\uC804 \uBC88\uD638\uB97C \uC785\uB825\uD558\uC138\uC694.'); return; }
  fetch('/api/versions/install', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: ver }),
  })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      alert(data.status ? (ver + ' \uC124\uCE58 \uC644\uB8CC') : ('\uC2E4\uD328: ' + (data.error || '')));
      loadVersions();
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

window.activateVersion = function(ver) {
  fetch('/api/versions/activate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: ver }),
  })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var msg = ver + ' \uD65C\uC131\uD654\uB428.';
      if (data.note) msg += '\n\u26A0\uFE0F ' + data.note;
      alert(msg);
      loadVersions();
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

// Fetch
function loadBots() {
  fetch('/api/bots')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      renderBots(data.bots || []);
      var bots    = data.bots || [];
      var failed  = bots.filter(function(b) { return b.state === 'failed'; }).length;
      var running = bots.filter(function(b) { return b.state === 'running'; }).length;
      var health  = failed > 0 ? 'degraded' : (running === 0 && bots.length > 0 ? 'down' : 'healthy');
      updateHealth(health);
      document.getElementById('last-updated').textContent = new Date().toLocaleTimeString('ko-KR');
    })
    .catch(function() { updateHealth('down'); });
}

function loadEvents() {
  fetch('/api/events')
    .then(function(r) { return r.json(); })
    .then(function(data) { renderEvents(data.events || []); })
    .catch(function() {});
}

function loadVersions() {
  fetch('/api/versions')
    .then(function(r) { return r.json(); })
    .then(function(data) { renderVersions(data); })
    .catch(function() {});
}

function loadConfig() {
  fetch('/api/config')
    .then(function(r) { return r.json(); })
    .then(function(data) { renderConfig(data); })
    .catch(function() {});
}

// --- Access management modal (per-bot) ---
var currentAccess = null;
var currentAccessBot = '';

window.showAccessModal = function(botId) {
  currentAccessBot = botId;
  document.getElementById('accessModalTitle').textContent = botId + ' \uC811\uADFC \uAD00\uB9AC';
  document.getElementById('modal-detect-results').innerHTML = '';
  var modal = document.getElementById('accessModal');
  modal.style.display = 'flex';
  loadAccess();
};

window.closeAccessModal = function() {
  document.getElementById('accessModal').style.display = 'none';
};

document.getElementById('accessModal').addEventListener('click', function(e) {
  if (e.target === this) closeAccessModal();
});

function loadAccess() {
  if (!currentAccessBot) return;
  fetch('/api/bots/' + currentAccessBot + '/access').then(function(r){return r.json()}).then(function(data) {
    currentAccess = data;
    var usersEl  = document.getElementById('modal-access-users');
    var groupsEl = document.getElementById('modal-access-groups');
    var policyEl = document.getElementById('modal-dm-policy');
    if (!usersEl) return;

    // DM policy
    if (policyEl && data.dmPolicy) policyEl.value = data.dmPolicy;

    // Users
    var allowFrom = data.allowFrom || [];
    if (allowFrom.length === 0) {
      usersEl.innerHTML = '<div style="color:#8b949e;font-size:12px">\uD5C8;&#xC6A9;\uB41C \uC0AC\uC6A9\uC790 \uC5C6\uC74C</div>';
    } else {
      usersEl.innerHTML = allowFrom.map(function(uid) {
        return '<div style="display:flex;justify-content:space-between;align-items:center;padding:3px 0;border-bottom:1px solid #21262d">'
          + '<span style="font-family:monospace;font-size:12px">' + esc(uid) + '</span>'
          + '<button onclick="removeUser(\'' + esc(uid) + '\')" style="padding:2px 6px;background:#da3633;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:11px">\uC0AD\uC81C</button>'
          + '</div>';
      }).join('');
    }

    // Groups
    var groups = data.groups || {};
    var gids = Object.keys(groups);
    if (gids.length === 0) {
      groupsEl.innerHTML = '<div style="color:#8b949e;font-size:12px">\uD5C8;\uC6A9;\uB41C \uADF8\uB8F9 \uC5C6\uC74C</div>';
    } else {
      groupsEl.innerHTML = gids.map(function(gid) {
        var g = groups[gid];
        var mention = g.requireMention ? '\uBA58\uC158 \uD544\uC694' : '\uBAA8\uB4E0 \uBA54\uC2DC\uC9C0';
        return '<div style="display:flex;justify-content:space-between;align-items:center;padding:3px 0;border-bottom:1px solid #21262d">'
          + '<span><span style="font-family:monospace;font-size:12px">' + esc(gid) + '</span> <span style="color:#8b949e;font-size:11px">(' + mention + ')</span></span>'
          + '<button onclick="removeGroup(\'' + esc(gid) + '\')" style="padding:2px 6px;background:#da3633;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:11px">\uC0AD\uC81C</button>'
          + '</div>';
      }).join('');
    }
  }).catch(function(){});
}

function addUser() {
  var uid = document.getElementById('modal-new-user-id').value.trim();
  if (!uid) return;
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'add_user', id:uid})
  }).then(function(r){return r.json()}).then(function() {
    document.getElementById('modal-new-user-id').value = '';
    loadAccess();
  });
}

function removeUser(uid) {
  if (!confirm(uid + ' \uC0AC\uC6A9\uC790\uB97C \uC81C\uAC70\uD558\uC2DC\uACA0\uC2B5\uB2C8\uAE4C?')) return;
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'remove_user', id:uid})
  }).then(function(){loadAccess()});
}

function addGroup() {
  var gid = document.getElementById('modal-new-group-id').value.trim();
  if (!gid) return;
  var mention = document.getElementById('modal-new-group-mention').checked;
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'add_group', id:gid, require_mention:mention})
  }).then(function(r){return r.json()}).then(function() {
    document.getElementById('modal-new-group-id').value = '';
    document.getElementById('modal-new-group-mention').checked = false;
    loadAccess();
  });
}

function removeGroup(gid) {
  if (!confirm(gid + ' \uADF8\uB8F9\uC744 \uC81C\uAC70\uD558\uC2DC\uACA0\uC2B5\uB2C8\uAE4C?')) return;
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'remove_group', id:gid})
  }).then(function(){loadAccess()});
}

function updateDmPolicy() {
  var policy = document.getElementById('modal-dm-policy').value;
  if (!currentAccessBot) return;
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'set_dm_policy', id:policy})
  }).then(function() {
    document.getElementById('modal-dm-policy-status').textContent = '\uC800\uC7A5\uB428 \u2713';
    setTimeout(function(){ document.getElementById('modal-dm-policy-status').textContent = ''; }, 2000);
    loadAccess();
  });
}

function detectGroups() {
  if (!currentAccessBot) return;
  var btn = event.target;
  btn.disabled = true;
  btn.textContent = '\uAC10\uC9C0 \uC911... (\uBD07 \uC7AC\uC2DC\uC791\uB428)';
  var resultsEl = document.getElementById('modal-detect-results');
  resultsEl.innerHTML = '<div style="color:#8b949e">\uBD07\uC744 \uBA48\uCD94\uACE0 \uB300\uAE30 \uC911\uC778 \uBA54\uC2DC\uC9C0\uB97C \uC218\uC9D1\uD569\uB2C8\uB2E4...</div>';

  fetch('/api/bots/' + currentAccessBot + '/access/detect', {method:'POST'})
    .then(function(r){return r.json()})
    .then(function(data) {
      btn.disabled = false;
      btn.textContent = '\uADF8\uB8F9/\uC0AC\uC6A9\uC790 \uAC10\uC9C0';
      var html = '';
      var groups = data.groups || [];
      var users = data.users || [];

      if (groups.length === 0 && users.length === 0) {
        resultsEl.innerHTML = '<div style="padding:12px;color:#8b949e;font-size:12px">\uAC10\uC9C0\uB41C \uADF8\uB8F9/\uC0AC\uC6A9\uC790 \uC5C6\uC74C. \uBD07\uC5D0\uAC8C \uBA54\uC2DC\uC9C0\uB97C \uBCF4\uB0B8 \uD6C4 \uB2E4\uC2DC \uC2DC\uB3C4\uD558\uC138\uC694.</div>';
        return;
      }

      if (groups.length > 0) {
        html += '<div style="background:#0d1117;border:1px solid #21262d;border-radius:8px;padding:12px;margin-bottom:8px"><h4 style="margin:0 0 8px;font-size:13px">\uAC10\uC9C0\uB41C \uADF8\uB8F9</h4>';
        groups.forEach(function(g) {
          var badge = g.known ? '<span style="color:#3fb950;font-size:11px">\uB4F1\uB85D\uB428</span>' : '<button onclick="addDetectedGroup(\'' + esc(g.id) + '\')" style="padding:2px 6px;background:#238636;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:11px">\uD5C8\uC6A9</button>';
          html += '<div style="display:flex;justify-content:space-between;align-items:center;padding:4px 0;border-bottom:1px solid #21262d">'
            + '<span><span style="font-family:monospace;font-size:12px">' + esc(g.id) + '</span> <span style="color:#e6edf3">' + esc(g.title) + '</span> <span style="color:#8b949e;font-size:11px">(' + g.type + ')</span></span>'
            + badge + '</div>';
        });
        html += '</div>';
      }

      if (users.length > 0) {
        html += '<div style="background:#0d1117;border:1px solid #21262d;border-radius:8px;padding:12px"><h4 style="margin:0 0 8px;font-size:13px">\uAC10\uC9C0\uB41C \uC0AC\uC6A9\uC790</h4>';
        users.forEach(function(u) {
          var badge = u.known ? '<span style="color:#3fb950;font-size:11px">\uB4F1\uB85D\uB428</span>' : '<button onclick="addDetectedUser(\'' + esc(u.id) + '\')" style="padding:2px 6px;background:#238636;border:none;border-radius:4px;color:#fff;cursor:pointer;font-size:11px">\uD5C8\uC6A9</button>';
          html += '<div style="display:flex;justify-content:space-between;align-items:center;padding:4px 0;border-bottom:1px solid #21262d">'
            + '<span><span style="font-family:monospace;font-size:12px">' + esc(u.id) + '</span> <span style="color:#e6edf3">' + esc(u.title) + '</span></span>'
            + badge + '</div>';
        });
        html += '</div>';
      }

      resultsEl.innerHTML = html;
    })
    .catch(function(e) {
      btn.disabled = false;
      btn.textContent = '\uADF8\uB8F9/\uC0AC\uC6A9\uC790 \uAC10\uC9C0';
      resultsEl.innerHTML = '<div style="padding:12px;color:#f85149">\uAC10\uC9C0 \uC2E4\uD328: ' + e + '</div>';
    });
}

function addDetectedGroup(gid) {
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'add_group', id:gid, require_mention:false})
  }).then(function(){loadAccess();});
}

function addDetectedUser(uid) {
  fetch('/api/bots/' + currentAccessBot + '/access', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action:'add_user', id:uid})
  }).then(function(){loadAccess();});
}

// Add Bot Modal
window.showAddBotModal = function() {
  document.getElementById('add-bot-id').value = '';
  document.getElementById('add-bot-name').value = '';
  document.getElementById('add-bot-token').value = '';
  document.getElementById('add-bot-model').value = '';
  document.getElementById('add-bot-system-prompt').value = '';
  document.getElementById('add-bot-type').value = 'telegram';
  document.getElementById('addBotModal').style.display = 'flex';
};

window.closeAddBotModal = function() {
  document.getElementById('addBotModal').style.display = 'none';
};

document.getElementById('addBotModal').addEventListener('click', function(e) {
  if (e.target === this) closeAddBotModal();
});

window.submitAddBot = function() {
  var id    = document.getElementById('add-bot-id').value.trim();
  var token = document.getElementById('add-bot-token').value.trim();
  if (!id || !token) { alert('ID\uC640 \uD1A0\uD070\uC740 \uD544\uC218\uC785\uB2C8\uB2E4.'); return; }
  var payload = {
    id:            id,
    type:          document.getElementById('add-bot-type').value,
    name:          document.getElementById('add-bot-name').value.trim(),
    token:         token,
    model:         document.getElementById('add-bot-model').value.trim(),
    system_prompt: document.getElementById('add-bot-system-prompt').value.trim(),
  };
  fetch('/api/bots', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.status === 'created') {
        closeAddBotModal();
        loadBots();
      } else {
        alert('\uC2E4\uD328: ' + (data.error || JSON.stringify(data)));
      }
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

window.deleteBot = function(id) {
  if (!confirm(id + ' \uBD07\uC744 \uC0AD\uC81C\uD558\uC2DC\uACA0\uC2B5\uB2C8\uAE4C?\n\uC774 \uC791\uC5C5\uC740 \uB418\uB3CC\uB9B4 \uC218 \uC5C6\uC2B5\uB2C8\uB2E4.')) return;
  fetch('/api/bots/' + encodeURIComponent(id), { method: 'DELETE' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.status === 'deleted') {
        loadBots();
      } else {
        alert('\uC2E4\uD328: ' + (data.error || JSON.stringify(data)));
      }
    })
    .catch(function(e) { alert('\uC624\uB958: ' + e); });
};

// Init and auto-refresh
loadBots();
setInterval(loadBots, 5000);
setInterval(function() {
  if (document.getElementById('pane-events').classList.contains('active')) loadEvents();
}, 5000);
setInterval(function() {
  if (document.getElementById('pane-troubleshoot').classList.contains('active')) {
    if (document.getElementById('ts-auto').checked) loadTroubleshoot();
  }
}, 5000);

})();
</script>
</body>
</html>`

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/your-org/claude-harness/internal/store"
	"github.com/your-org/claude-harness/internal/supervisor"
)

type Server struct {
	sv    *supervisor.Supervisor
	store store.Store
	addr  string
}

func NewServer(addr string, sv *supervisor.Supervisor, st store.Store) *Server {
	return &Server{sv: sv, store: st, addr: addr}
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
		// TODO: implement per-bot restart in supervisor
		writeJSON(w, map[string]string{"status": "restart requested", "bot": botID})
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
	// TODO: wire to version.Manager when available
	writeJSON(w, map[string]interface{}{
		"versions": []map[string]interface{}{
			{"version": "system", "active": true},
		},
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
		// List all channels with conversations
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
		// List conversations for a channel
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

	// Get a single conversation
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
<title>Claude Harness v4.0 — 대시보드</title>
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

/* ── Header ── */
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
.header-title span { color: var(--text2); font-weight: 400; font-size: 13px; margin-left: 4px; }
.header-right { display: flex; align-items: center; gap: 12px; }
.refresh-info { font-size: 12px; color: var(--text3); }
#last-updated { color: var(--text2); }

.health-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  padding: 4px 12px;
  border-radius: 20px;
  border: 1px solid;
}
.health-badge.healthy  { color: var(--green);  background: var(--green-bg);  border-color: rgba(63,185,80,.3); }
.health-badge.degraded { color: var(--yellow); background: var(--yellow-bg); border-color: rgba(210,153,34,.3); }
.health-badge.down     { color: var(--red);    background: var(--red-bg);    border-color: rgba(248,81,73,.3); }
.health-badge .dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.health-badge.healthy .dot  { animation: pulse 2s infinite; }

@keyframes pulse {
  0%,100% { opacity:1; }
  50%      { opacity:.35; }
}

/* ── Tabs ── */
.tabs {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  padding: 0 24px;
  display: flex;
  gap: 0;
}
.tab {
  padding: 12px 18px;
  cursor: pointer;
  color: var(--text2);
  border-bottom: 2px solid transparent;
  font-size: 13px;
  font-weight: 500;
  transition: color .15s, border-color .15s;
  user-select: none;
}
.tab:hover  { color: var(--text); }
.tab.active { color: var(--text); border-bottom-color: var(--blue); }

/* ── Layout ── */
.container { max-width: 1140px; margin: 0 auto; padding: 28px 24px; }

.tab-pane { display: none; }
.tab-pane.active { display: block; }

/* ── Summary row ── */
.summary-row {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 28px;
}
.summary-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.summary-card .s-val {
  font-size: 28px;
  font-weight: 700;
  line-height: 1;
}
.summary-card .s-lbl {
  font-size: 12px;
  color: var(--text2);
  font-weight: 500;
}
.summary-card.green .s-val { color: var(--green); }
.summary-card.yellow .s-val { color: var(--yellow); }
.summary-card.red .s-val { color: var(--red); }
.summary-card.blue .s-val { color: var(--blue); }

/* ── Section title ── */
.section-title {
  font-size: 11px;
  font-weight: 700;
  color: var(--text3);
  text-transform: uppercase;
  letter-spacing: .8px;
  margin: 0 0 12px;
}

/* ── Bot grid ── */
.bot-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 14px;
}

.bot-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
  transition: border-color .15s;
}
.bot-card:hover { border-color: #444c56; }

.bot-card-header {
  padding: 14px 16px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  border-bottom: 1px solid var(--border2);
}
.bot-name-row { display: flex; flex-direction: column; gap: 3px; }
.bot-name { font-size: 15px; font-weight: 600; }
.bot-id   { font-size: 11px; color: var(--text3); font-family: monospace; }

.state-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 20px;
  border: 1px solid;
  white-space: nowrap;
  flex-shrink: 0;
}
.state-badge.running { color: var(--green);  background: var(--green-bg);  border-color: rgba(63,185,80,.3); }
.state-badge.failed  { color: var(--red);    background: var(--red-bg);    border-color: rgba(248,81,73,.3); }
.state-badge.idle,
.state-badge.stopped { color: var(--text2);  background: var(--surface2);  border-color: var(--border); }

.bot-metrics {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0;
  border-bottom: 1px solid var(--border2);
}
.bot-metric {
  padding: 10px 14px;
  text-align: center;
  border-right: 1px solid var(--border2);
}
.bot-metric:last-child { border-right: none; }
.bot-metric .m-val { font-size: 18px; font-weight: 700; }
.bot-metric .m-lbl { font-size: 10px; color: var(--text2); margin-top: 1px; }

.bot-footer {
  padding: 10px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.bot-type {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--surface2);
  color: var(--text2);
  border: 1px solid var(--border);
  font-family: monospace;
}
.bot-channels-count { font-size: 12px; color: var(--text3); }

/* channel sub-list inside bot card */
.channel-list {
  padding: 0 16px 10px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.ch-chip {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--blue-bg);
  color: var(--blue);
  border: 1px solid rgba(88,166,255,.2);
  font-family: monospace;
}

/* ── Events table ── */
.events-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}
.events-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.events-table th {
  text-align: left;
  padding: 10px 14px;
  color: var(--text2);
  border-bottom: 1px solid var(--border);
  font-weight: 600;
  font-size: 12px;
  background: var(--surface2);
}
.events-table td {
  padding: 8px 14px;
  border-bottom: 1px solid var(--border2);
  vertical-align: middle;
}
.events-table tr:last-child td { border-bottom: none; }
.events-table tr:hover td { background: var(--surface2); }
.time-cell  { color: var(--text3); white-space: nowrap; font-size: 12px; font-family: monospace; }
.id-cell    { color: var(--text2); font-family: monospace; font-size: 12px; }

.ev-tag {
  display: inline-block;
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 4px;
  text-transform: lowercase;
}
.ev-tag.started   { color: var(--green);  background: var(--green-bg); }
.ev-tag.stopped   { color: var(--yellow); background: var(--yellow-bg); }
.ev-tag.failed,
.ev-tag.error     { color: var(--red);    background: var(--red-bg); }
.ev-tag.restarted { color: var(--blue);   background: var(--blue-bg); }
.ev-tag.message   { color: var(--text2);  background: var(--surface2); }

.empty-state {
  padding: 48px;
  text-align: center;
  color: var(--text3);
  font-size: 13px;
}

@media (max-width: 700px) {
  .summary-row { grid-template-columns: repeat(2, 1fr); }
  .bot-grid    { grid-template-columns: 1fr; }
  .container   { padding: 16px; }
}
</style>
</head>
<body>

<!-- ═══ Header ═══ -->
<header class="header">
  <div class="header-left">
    <div class="header-title">🤖 Claude Harness <span>v4.0</span></div>
  </div>
  <div class="header-right">
    <span class="refresh-info">마지막 갱신: <span id="last-updated">—</span></span>
    <span class="health-badge healthy" id="health-badge"><span class="dot"></span><span id="health-text">정상</span></span>
  </div>
</header>

<!-- ═══ Tabs ═══ -->
<nav class="tabs">
  <div class="tab active" data-tab="bots">봇 목록</div>
  <div class="tab"        data-tab="events">이벤트 로그</div>
</nav>

<!-- ═══ Main ═══ -->
<main class="container">

  <!-- Bots tab -->
  <div class="tab-pane active" id="pane-bots">
    <!-- Summary row -->
    <div class="summary-row">
      <div class="summary-card blue">
        <div class="s-val" id="sum-total">—</div>
        <div class="s-lbl">전체 봇</div>
      </div>
      <div class="summary-card green">
        <div class="s-val" id="sum-running">—</div>
        <div class="s-lbl">실행 중</div>
      </div>
      <div class="summary-card red">
        <div class="s-val" id="sum-failed">—</div>
        <div class="s-lbl">오류</div>
      </div>
      <div class="summary-card yellow">
        <div class="s-val" id="sum-channels">—</div>
        <div class="s-lbl">전체 채널</div>
      </div>
    </div>

    <div class="section-title">봇 상태</div>
    <div class="bot-grid" id="bot-grid">
      <div class="empty-state">데이터 로딩 중…</div>
    </div>
  </div>

  <!-- Events tab -->
  <div class="tab-pane" id="pane-events">
    <div class="section-title">최근 이벤트 (최대 20개)</div>
    <div class="events-card">
      <table class="events-table">
        <thead>
          <tr>
            <th style="width:90px">시간</th>
            <th style="width:110px">경과</th>
            <th style="width:160px">봇 / 채널</th>
            <th style="width:90px">액션</th>
            <th>상세</th>
          </tr>
        </thead>
        <tbody id="events-body">
          <tr><td colspan="5" class="empty-state">이벤트 없음</td></tr>
        </tbody>
      </table>
    </div>
  </div>

</main>

<script>
(function () {
  'use strict';

  // ── Tab switching ──
  document.querySelectorAll('.tab').forEach(function(tab) {
    tab.addEventListener('click', function() {
      var name = tab.dataset.tab;
      document.querySelectorAll('.tab').forEach(function(t) {
        t.classList.toggle('active', t.dataset.tab === name);
      });
      document.querySelectorAll('.tab-pane').forEach(function(p) {
        p.classList.toggle('active', p.id === 'pane-' + name);
      });
      if (name === 'events') loadEvents();
    });
  });

  // ── Helpers ──
  function esc(s) {
    var d = document.createElement('div');
    d.textContent = String(s == null ? '' : s);
    return d.innerHTML;
  }

  function relTime(isoStr) {
    if (!isoStr) return '';
    var diff = (Date.now() - new Date(isoStr).getTime()) / 1000;
    if (diff < 5)   return '방금';
    if (diff < 60)  return Math.floor(diff) + '초 전';
    if (diff < 3600) return Math.floor(diff / 60) + '분 전';
    if (diff < 86400) return Math.floor(diff / 3600) + '시간 전';
    return Math.floor(diff / 86400) + '일 전';
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
    var map = { running: '실행 중', failed: '오류', idle: '유휴', stopped: '중지됨' };
    return map[state] || state || '—';
  }

  // ── Render bots ──
  function renderBots(bots) {
    var grid = document.getElementById('bot-grid');
    if (!bots || !bots.length) {
      grid.innerHTML = '<div class="empty-state">봇이 없습니다.</div>';
      return;
    }

    var totalChannels = 0;
    var running = 0, failed = 0;
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

      var channels = (b.channels || []).map(function(ch) {
        return '<span class="ch-chip">' + esc(ch.id || ch) + '</span>';
      }).join('');
      var channelSection = channels
        ? '<div class="channel-list">' + channels + '</div>'
        : '';

      return [
        '<div class="bot-card">',
          '<div class="bot-card-header">',
            '<div class="bot-name-row">',
              '<div class="bot-name">' + name + '</div>',
              id ? '<div class="bot-id">' + id + '</div>' : '',
            '</div>',
            '<span class="state-badge ' + cls + '">' + label + '</span>',
          '</div>',
          '<div class="bot-metrics">',
            '<div class="bot-metric"><div class="m-val">' + esc(b.uptime || '—') + '</div><div class="m-lbl">업타임</div></div>',
            '<div class="bot-metric"><div class="m-val">' + esc(b.restart_count != null ? b.restart_count : '—') + '</div><div class="m-lbl">재시작</div></div>',
            '<div class="bot-metric"><div class="m-val">' + esc(b.channel_count != null ? b.channel_count : '—') + '</div><div class="m-lbl">채널</div></div>',
          '</div>',
          channelSection,
          '<div class="bot-footer">',
            '<span class="bot-type">' + esc(b.type || 'unknown') + '</span>',
            b.channel_count ? '<span class="bot-channels-count">' + esc(b.channel_count) + '개 채널 활성</span>' : '',
          '</div>',
        '</div>',
      ].join('');
    }).join('');
  }

  // ── Render events ──
  function renderEvents(events) {
    var tbody = document.getElementById('events-body');
    var list  = (events || []).slice().reverse().slice(0, 20);
    if (!list.length) {
      tbody.innerHTML = '<tr><td colspan="5" class="empty-state">이벤트 없음</td></tr>';
      return;
    }
    tbody.innerHTML = list.map(function(ev) {
      var action = (ev.action || 'message').toLowerCase();
      var tagCls = ['started','stopped','failed','error','restarted'].indexOf(action) >= 0 ? action : 'message';
      return [
        '<tr>',
          '<td class="time-cell">'    + esc(timeStr(ev.time)) + '</td>',
          '<td class="time-cell">'    + esc(relTime(ev.time)) + '</td>',
          '<td class="id-cell">'      + esc(ev.bot_id || ev.channel_id || '—') + '</td>',
          '<td><span class="ev-tag ' + tagCls + '">' + esc(action) + '</span></td>',
          '<td>'                      + esc(ev.detail || '') + '</td>',
        '</tr>',
      ].join('');
    }).join('');
  }

  // ── Update health badge ──
  function updateHealth(status) {
    var badge = document.getElementById('health-badge');
    var text  = document.getElementById('health-text');
    var map   = { healthy: ['healthy','정상'], degraded: ['degraded','일부 오류'], down: ['down','다운'] };
    var info  = map[status] || map['healthy'];
    badge.className = 'health-badge ' + info[0];
    text.textContent = info[1];
  }

  // ── Fetch helpers ──
  function loadBots() {
    fetch('/api/bots')
      .then(function(r) { return r.json(); })
      .then(function(data) {
        renderBots(data.bots || []);
        // derive health from bot states
        var bots = data.bots || [];
        var failed  = bots.filter(function(b) { return b.state === 'failed'; }).length;
        var running = bots.filter(function(b) { return b.state === 'running'; }).length;
        var health  = failed > 0 ? 'degraded' : (running === 0 && bots.length > 0 ? 'down' : 'healthy');
        updateHealth(health);
        document.getElementById('last-updated').textContent = new Date().toLocaleTimeString('ko-KR');
      })
      .catch(function() {
        updateHealth('down');
      });
  }

  function loadEvents() {
    fetch('/api/events')
      .then(function(r) { return r.json(); })
      .then(function(data) { renderEvents(data.events || []); })
      .catch(function() {});
  }

  // ── Init & auto-refresh ──
  loadBots();
  setInterval(loadBots, 10000);
  // refresh events too if that tab is active
  setInterval(function() {
    if (document.getElementById('pane-events').classList.contains('active')) {
      loadEvents();
    }
  }, 10000);

})();
</script>
</body>
</html>`

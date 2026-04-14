# Claude Channel Hub

**English** | [中文](README.zh.md) | [한국어](README.md)

A multi-channel bot orchestrator leveraging Claude Code's official [Channels](https://code.claude.com/docs/ko/channels) feature.
Manages multiple bots simultaneously and automatically monitors/recovers each bot's Claude Code process.

## Architecture

```
┌──────────────────────────────────────────────────┐
│              Go Harness (Supervisor)              │
│                                                   │
│  Bot Manager ─── Version Manager ─── Admin API   │
└──────┬──────────────────┬──────────────┬─────────┘
       │                  │              │
  ┌────┴─────┐      ┌────┴─────┐   ┌────┴─────┐
  │ tmux     │      │ tmux     │   │ tmux     │
  │ cch-bot1 │      │ cch-bot2 │   │ cch-botN │
  │          │      │          │   │          │
  │ claude   │      │ claude   │   │ claude   │
  │ --danger │      │ --danger │   │ --danger │
  │ ously-   │      │ ously-   │   │ ously-   │
  │ load-dev │      │ load-dev │   │ load-dev │
  │ -channels│      │ -channels│   │ -channels│
  └────┬─────┘      └────┬─────┘   └──────────┘
       │                  │
   Telegram            Telegram
   @bot1               @bot2
   ┌────────┐          ┌────────┐
   │Direct  │          │Group   │
   │Group   │          │Chat    │
   └────────┘          └────────┘
```

### Core Components

| Component | Role |
|----------|------|
| **Supervisor** | Manages tmux sessions per bot, health checks, auto-restart (exponential backoff) |
| **Bot Manager** | Bot CRUD, channel routing (1 bot : N channels supported) |
| **Version Manager** | Install/switch Claude Code versions, per-bot version assignment |
| **Channel Plugin** | MCP server-based message bridge (memory, profile, skills extensions) |
| **Admin Dashboard** | HTTP dashboard + REST API |

### Bot and Channel Relationship

- **Bot** = Platform bot token + 1 tmux session (claude process)
- **Channel** = Logical conversation space (group, DM, topic, etc.)
- Supports 1 bot:1 channel (isolated), 1 bot:N channels (unified), and mixed configurations

## Quick Start

### Prerequisites

- Go 1.22+
- [Claude Code](https://claude.ai/code) (claude.ai login required)
- [Bun](https://bun.sh) (for running channel plugins)
- tmux (for bot process session management)
- Telegram bot token (issued via [BotFather](https://t.me/BotFather))

### 1. Configuration

```bash
git clone https://github.com/ez2k/claude-channel-hub.git
cd claude-channel-hub

cp .env.example .env
# Enter TELEGRAM_BOT_TOKEN
vim .env
```

### 2. Build & Run

```bash
make build
make run
# → Bot process starts (tmux session: cch-{botId})
# → Dashboard: http://localhost:8082
```

### 3. Install Script (includes systemd service)

```bash
sudo bash install.sh
sudo systemctl start claude-channel-hub
```

### 4. Docker

```bash
make docker-build
make docker-run
```

## Configuration (channels.yaml)

```yaml
admin:
  addr: ":8082"

supervisor:
  health_check_interval: 30s
  max_restarts: 10
  restart_delay: 2s
  restart_backoff_max: 5m

claude:
  default_version: "latest"
  auto_update: false

defaults:
  plugin: telegram-enhanced
  plugin_dir: ./plugins/telegram-enhanced

bots:
  - id: main-bot
    type: telegram
    name: "Main Bot"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: telegram-enhanced
    plugin_dir: ./plugins/telegram-enhanced

  # - id: second-bot
  #   type: telegram
  #   name: "Second Bot"
  #   enabled: false
  #   token: "${TELEGRAM_SECOND_TOKEN}"
  #   plugin: telegram-enhanced
  #   plugin_dir: ./plugins/telegram-enhanced

channels:
  - id: general
    bot: main-bot
    name: "General Chat"
    match:
      type: default               # All messages not matched by other channels
    data_dir: "./data/general"

  # Apply different settings to a specific group
  # - id: code-review
  #   bot: main-bot
  #   name: "Code Review Only"
  #   match:
  #     type: group
  #     chat_ids: ["-100123456789"]
  #   model: "claude-sonnet-4-6"
  #   system_prompt: "Respond as a code review expert."
  #   data_dir: "./data/code-review"
```

### Channel Routing Rules

| match.type | Description | Example |
|---|---|---|
| `default` | All messages not matched by other channels | DM, unspecified groups |
| `group` | Specific Telegram group | `chat_ids: ["-100xxx"]` |
| `user` | Specific user DM | `user_ids: ["12345"]` |

## Process Management

The Supervisor manages each bot as a tmux session:

- **tmux session**: Creates a session `cch-{botId}` per bot ID, runs independently
- **Channel mode**: `--dangerously-load-development-channels server:telegram-enhanced`
- **Working directory**: `~/.claude-channel-hub/data/{botId}/` (isolated per bot)
- **Bot state**: `~/.claude/channels/telegram-{botId}/` (access.json, .env)
- **Health check**: Checks tmux session status every 30 seconds + detects 10-minute idle → auto-restart
- **Auto-restart**: Exponential backoff on crash (2s → 4s → 8s → ... → max 5 minutes)
- **Prompt auto-response**: tmux send-keys Enter (auto-confirms development channel warnings)
- **Zombie process cleanup**: Auto-kills bun processes with the same token (prevents Telegram 409)
- **Logs**: `/tmp/claude-bot-{botId}.log` (tmux pipe-pane)
- **Session resume**: Automatically applies `--continue` flag if a previous session exists

## Admin Dashboard

Accessible at `http://localhost:8082`:

- Sidebar layout (bot list + detail panel)
- Per-bot status (running/stopped/failed), uptime, restart count
- Bot CRUD (add/edit/delete)
- Access management modal (access.json editor)
- Memory viewer
- tmux session name display

### REST API

```
GET  /api/bots                # Bot list + status
GET  /api/bots/:id            # Single bot status
POST /api/bots                # Add bot
PUT  /api/bots/:id            # Edit bot
DELETE /api/bots/:id          # Delete bot
GET  /api/bots/:id/channels   # Bot's channel list
POST /api/bots/:id/restart    # Restart bot
GET  /api/bots/:id/logs       # Bot logs (/tmp/claude-bot-{id}.log)
GET  /api/channels            # All channels list
GET  /api/versions            # Claude Code version list
GET  /api/status              # System status
GET  /api/events              # Event log
GET  /api/health              # Health check
```

## telegram-enhanced Plugin

`plugins/telegram-enhanced/server.ts` — single self-contained file (no external ./src/ imports):

- **Memory system**: FTS search, similarity-based deduplication, weighting, auto-extraction
- **User profiles**: Language detection (Korean/English/Japanese), style analysis, topic tracking
- **MCP tools**: `memory_recall`, `memory_save`, `memory_stats`, `profile_get`, `profile_update`
- **Channel routing**: Apply per-channel settings via `HARNESS_CHANNELS_CONFIG` environment variable

## Data Paths

```
~/.claude-channel-hub/
  data/
    {botId}/                    # Bot working directory (HARNESS_DATA_DIR)
      .mcp.json                 # Plugin MCP config (auto-copied)
      .claude/sessions/         # Claude session files

~/.claude/channels/
  telegram-{botId}/             # Bot state directory
    access.json                 # Authentication/pairing
    bot.pid                     # Process ID
    .env                        # Bot token

/tmp/claude-bot-{botId}.log     # Bot log (tmux pipe-pane)
```

## Project Structure

```
claude-channel-hub/
├── cmd/bot/main.go                 # Entry point
├── configs/channels.yaml           # Bot/channel configuration
├── internal/
│   ├── admin/server.go             # HTTP dashboard + REST API
│   ├── bot/
│   │   ├── bot.go                  # Bot definition
│   │   └── process.go              # tmux session-based process management
│   ├── config/config.go            # YAML config loader
│   ├── supervisor/supervisor.go    # Bot process monitoring
│   └── version/manager.go         # Claude Code version management
├── plugins/
│   └── telegram-enhanced/          # Telegram channel plugin
│       ├── server.ts               # MCP server (single file, self-contained)
│       └── package.json
├── docs/
│   └── ARCHITECTURE.md             # Detailed architecture
├── claude-channel-hub.service      # systemd service
├── install.sh                      # Install script
├── Dockerfile
└── Makefile
```

## License

MIT

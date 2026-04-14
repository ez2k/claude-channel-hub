# Claude Channel Hub

[English](README.en.md) | **中文** | [한국어](README.md)

一个利用 Claude Code 官方 [Channels](https://code.claude.com/docs/ko/channels) 功能的多渠道机器人编排器。
可同时管理多个机器人，并自动监控/恢复每个机器人的 Claude Code 进程。

## 架构

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
   │私聊 DM │          │群组聊天│
   │群组聊天│          └────────┘
   └────────┘
```

### 核心组件

| 组件 | 功能 |
|----------|------|
| **Supervisor** | 管理每个机器人的 tmux 会话、健康检查、自动重启（指数退避） |
| **Bot Manager** | 机器人 CRUD、渠道路由（支持 1 机器人:N 渠道） |
| **Version Manager** | 安装/切换 Claude Code 版本，按机器人指定版本 |
| **Channel Plugin** | 基于 MCP 服务器的消息桥接（内存、配置文件、技能扩展） |
| **Admin Dashboard** | HTTP 控制面板 + REST API |

### 机器人与渠道的关系

- **Bot（机器人）** = 平台机器人令牌 + 1 个 tmux 会话（claude 进程）
- **Channel（渠道）** = 逻辑对话空间（群组、私聊、话题等）
- 支持 1 机器人:1 渠道（隔离）、1 机器人:N 渠道（统一）以及混合配置

## 快速开始

### 前置要求

- Go 1.22+
- [Claude Code](https://claude.ai/code)（需要登录 claude.ai）
- [Bun](https://bun.sh)（用于运行渠道插件）
- tmux（用于机器人进程会话管理）
- Telegram 机器人令牌（通过 [BotFather](https://t.me/BotFather) 获取）

### 1. 配置

```bash
git clone https://github.com/ez2k/claude-channel-hub.git
cd claude-channel-hub

cp .env.example .env
# 填入 TELEGRAM_BOT_TOKEN
vim .env
```

### 2. 构建 & 运行

```bash
make build
make run
# → 机器人进程启动（tmux 会话：cch-{botId}）
# → 控制面板：http://localhost:8082
```

### 3. 安装脚本（含 systemd 服务）

```bash
sudo bash install.sh
sudo systemctl start claude-channel-hub
```

### 4. Docker

```bash
make docker-build
make docker-run
```

## 配置（channels.yaml）

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
    name: "主机器人"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: telegram-enhanced
    plugin_dir: ./plugins/telegram-enhanced

  # - id: second-bot
  #   type: telegram
  #   name: "第二个机器人"
  #   enabled: false
  #   token: "${TELEGRAM_SECOND_TOKEN}"
  #   plugin: telegram-enhanced
  #   plugin_dir: ./plugins/telegram-enhanced

channels:
  - id: general
    bot: main-bot
    name: "通用对话"
    match:
      type: default               # 所有未被其他渠道匹配的消息
    data_dir: "./data/general"

  # 对特定群组应用不同配置
  # - id: code-review
  #   bot: main-bot
  #   name: "专属代码审查"
  #   match:
  #     type: group
  #     chat_ids: ["-100123456789"]
  #   model: "claude-sonnet-4-6"
  #   system_prompt: "请作为代码审查专家进行回答。"
  #   data_dir: "./data/code-review"
```

### 渠道路由规则

| match.type | 说明 | 示例 |
|---|---|---|
| `default` | 未被其他渠道匹配的所有消息 | 私聊、未指定群组 |
| `group` | 特定 Telegram 群组 | `chat_ids: ["-100xxx"]` |
| `user` | 特定用户私聊 | `user_ids: ["12345"]` |

## 进程管理

Supervisor 将每个机器人作为 tmux 会话进行管理：

- **tmux 会话**：按机器人 ID 创建会话 `cch-{botId}`，独立运行
- **渠道模式**：`--dangerously-load-development-channels server:telegram-enhanced`
- **工作目录**：`~/.claude-channel-hub/data/{botId}/`（按机器人隔离）
- **机器人状态**：`~/.claude/channels/telegram-{botId}/`（access.json、.env）
- **健康检查**：每 30 秒检查 tmux 会话状态 + 检测 10 分钟空闲 → 自动重启
- **自动重启**：崩溃时指数退避（2s → 4s → 8s → ... → 最大 5 分钟）
- **提示词自动响应**：tmux send-keys Enter（自动确认开发渠道警告）
- **僵尸进程清理**：自动 kill 相同令牌的 bun 进程（防止 Telegram 409）
- **日志**：`/tmp/claude-bot-{botId}.log`（tmux pipe-pane）
- **会话恢复**：如果存在上一个会话，自动应用 `--continue` 标志

## Admin 控制面板

在 `http://localhost:8082` 访问：

- 侧边栏布局（机器人列表 + 详情面板）
- 每个机器人的状态（running/stopped/failed）、运行时间、重启次数
- 机器人 CRUD（添加/编辑/删除）
- 访问管理弹窗（access.json 编辑）
- 内存查看器
- tmux 会话名称显示

### REST API

```
GET  /api/bots                # 机器人列表 + 状态
GET  /api/bots/:id            # 单个机器人状态
POST /api/bots                # 添加机器人
PUT  /api/bots/:id            # 编辑机器人
DELETE /api/bots/:id          # 删除机器人
GET  /api/bots/:id/channels   # 机器人的渠道列表
POST /api/bots/:id/restart    # 重启机器人
GET  /api/bots/:id/logs       # 机器人日志（/tmp/claude-bot-{id}.log）
GET  /api/channels            # 全部渠道列表
GET  /api/versions            # Claude Code 版本列表
GET  /api/status              # 系统状态
GET  /api/events              # 事件日志
GET  /api/health              # 健康检查
```

## telegram-enhanced 插件

`plugins/telegram-enhanced/server.ts` — 单一自完备文件（无外部 ./src/ 导入）：

- **内存系统**：FTS 搜索、基于相似度的去重、权重、自动提取
- **用户配置文件**：语言检测（韩/英/日）、风格分析、话题追踪
- **MCP 工具**：`memory_recall`、`memory_save`、`memory_stats`、`profile_get`、`profile_update`
- **渠道路由**：通过 `HARNESS_CHANNELS_CONFIG` 环境变量应用每个渠道的配置

## 数据路径

```
~/.claude-channel-hub/
  data/
    {botId}/                    # 机器人工作目录（HARNESS_DATA_DIR）
      .mcp.json                 # 插件 MCP 配置（自动复制）
      .claude/sessions/         # Claude 会话文件

~/.claude/channels/
  telegram-{botId}/             # 机器人状态目录
    access.json                 # 认证/配对
    bot.pid                     # 进程 ID
    .env                        # 机器人令牌

/tmp/claude-bot-{botId}.log     # 机器人日志（tmux pipe-pane）
```

## 项目结构

```
claude-channel-hub/
├── cmd/bot/main.go                 # 入口点
├── configs/channels.yaml           # 机器人/渠道配置
├── internal/
│   ├── admin/server.go             # HTTP 控制面板 + REST API
│   ├── bot/
│   │   ├── bot.go                  # Bot 定义
│   │   └── process.go              # 基于 tmux 会话的进程管理
│   ├── config/config.go            # YAML 配置加载器
│   ├── supervisor/supervisor.go    # 机器人进程监控
│   └── version/manager.go         # Claude Code 版本管理
├── plugins/
│   └── telegram-enhanced/          # Telegram 渠道插件
│       ├── server.ts               # MCP 服务器（单文件，自完备）
│       └── package.json
├── docs/
│   └── ARCHITECTURE.md             # 详细架构说明
├── claude-channel-hub.service      # systemd 服务
├── install.sh                      # 安装脚本
├── Dockerfile
└── Makefile
```

## 许可证

MIT

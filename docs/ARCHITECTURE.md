# 아키텍처 — Claude Channel Hub

## 개요

Go 프로세스가 Claude Code 봇 프로세스들을 **tmux 세션**으로 관리하는 오케스트레이터 구조.
각 봇은 독립적인 tmux 세션(`cch-{botId}`) 안에서 `claude --dangerously-load-development-channels` 로 실행된다.

---

## 봇과 채널의 관계

```
┌─────── 모드 A: 1봇 = 1채널 (분리) ───────┐
│                                            │
│  Bot (@main_bot)  ──→  Channel: tg-main   │
│  Bot (@beta_bot)  ──→  Channel: tg-beta   │
│                                            │
│  각 봇이 독립 tmux 세션으로 실행            │
│  완전한 격리 (작업 디렉토리, 설정 분리)     │
└────────────────────────────────────────────┘

┌─────── 모드 B: 1봇 = N채널 (통합) ───────┐
│                                            │
│  Bot (@main_bot)  ──┬→  Channel: 공개그룹  │
│                     ├→  Channel: 관리자DM  │
│                     └→  Channel: 스터디방   │
│                                            │
│  하나의 tmux 세션 + Claude 프로세스에서    │
│  HARNESS_CHANNELS_CONFIG 기반 채널 라우팅  │
│  채널별 설정 (모델, 프롬프트) 분기 가능    │
└────────────────────────────────────────────┘

┌─────── 모드 C: 혼합 ─────────────────────┐
│                                            │
│  Bot (@main_bot)  ──┬→  일반 대화 채널     │
│                     └→  코드리뷰 채널      │
│  Bot (@admin_bot) ──→   관리 전용 채널     │
│                                            │
│  봇 단위로 tmux 세션 분리                  │
│  봇 내에서 채널 라우팅                     │
└────────────────────────────────────────────┘
```

핵심 개념:
- **Bot**: 플랫폼 봇 (토큰 1개 = tmux 세션 1개 = claude 프로세스 1개)
- **Channel**: 논리적 대화 공간 (그룹, DM, 토픽 등)
- 하나의 봇이 여러 채널을 라우팅하거나, 봇을 분리하여 독립 운영 가능

---

## 전체 구조

```
┌──────────────────────────────────────────────────────────────────────┐
│                      Go Harness (Supervisor)                          │
│                                                                       │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │                    Bot & Channel Manager                        │ │
│  │                                                                  │ │
│  │  • Bot CRUD (봇 추가/삭제/토큰 관리)                              │ │
│  │  • Channel CRUD (채널 추가/삭제/라우팅 규칙 설정)                  │ │
│  │  • 채널별 설정 (모델, 시스템 프롬프트, 데이터 경로)                │ │
│  │  • Bot → Channel 매핑 관리 (1:1 또는 1:N)                        │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                                                                       │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │                   Process Supervisor                            │ │
│  │                                                                  │ │
│  │  • 봇 단위로 tmux 세션 생성/종료 (봇 1개 = 세션 1개)            │ │
│  │  • 헬스체크 (tmux 세션 alive + 10분 idle 감지)                  │ │
│  │  • 크래시 감지 → exponential backoff 재시작                      │ │
│  │  • 로그 캡처 (/tmp/claude-bot-{id}.log via tmux pipe-pane)      │ │
│  │  • 좀비 프로세스 정리 (killStaleBotProcesses)                    │ │
│  └────┬──────────────────┬──────────────────┬──────────────────────┘ │
│       │                  │                  │                        │
│  ┌────┴──────┐     ┌────┴──────┐     ┌────┴──────┐                  │
│  │ tmux sess │     │ tmux sess │     │ tmux sess │                  │
│  │ cch-bot1  │     │ cch-bot2  │     │ cch-botN  │                  │
│  └────┬──────┘     └────┬──────┘     └────┬──────┘                  │
│       │                  │                  │                        │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │              Claude Code Version Manager                        │ │
│  │  • 버전 설치/전환/자동 업데이트 • 봇별 버전 지정 가능            │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │       Admin Server (HTTP API + Dashboard, 사이드바 레이아웃)     │ │
│  │  • REST API: 봇/채널 CRUD, 상태, 로그, 버전 관리                │ │
│  │  • 봇 CRUD, 접근 관리 모달, 메모리 뷰어                         │ │
│  └─────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
         │                  │                  │
         ▼                  ▼                  ▼
┌────────────────┐ ┌────────────────┐ ┌────────────────┐
│ claude          │ │ claude          │ │ claude          │
│ --dangerously   │ │ --dangerously   │ │ --dangerously   │
│ -skip-perm      │ │ -skip-perm      │ │ -skip-perm      │
│ --dangerously   │ │ --dangerously   │ │ --dangerously   │
│ -load-dev-ch    │ │ -load-dev-ch    │ │ -load-dev-ch    │
│ server:tg-enh.  │ │ server:tg-enh.  │ │ server:tg-enh.  │
│                 │ │                 │ │                 │
│ MCP Server:     │ │ MCP Server:     │ │ MCP Server:     │
│ telegram-enh.   │ │ telegram-enh.   │ │ telegram-enh.   │
│                 │ │                 │ │                 │
│ Channel Router: │ │ (단일 채널)     │ │ Channel Router: │
│ ┌─────────────┐│ │                 │ │ ┌─────────────┐│
│ │general (DM) ││ │                 │ │ │general      ││
│ │code-review  ││ │                 │ │ │dev-channel  ││
│ └─────────────┘│ │                 │ │ └─────────────┘│
└───────┬────────┘ └───────┬────────┘ └───────┬────────┘
        │                  │                  │
        ▼                  ▼                  ▼
    Telegram            Telegram           Telegram
   @main_bot           @beta_bot          @other_bot
```

---

## 프로세스 관리 흐름

### 봇 시작

```
Go Harness
    │
    │ 1. channels.yaml에서 봇/채널 설정 로드
    │ 2. 봇 작업 디렉토리 준비
    │    ~/.claude-channel-hub/data/{botId}/
    │ 3. plugin_dir/.mcp.json → 작업 디렉토리에 복사
    │ 4. 이전 세션 존재 여부 확인 (--continue 결정)
    ▼
tmux new-session -d -s cch-{botId}
    │
    │ 환경변수 설정:
    │   unset ANTHROPIC_API_KEY
    │   export TELEGRAM_BOT_TOKEN=xxx
    │   export TELEGRAM_STATE_DIR=~/.claude/channels/telegram-{botId}/
    │   export HARNESS_CHANNELS_CONFIG='[...]'
    │   export HARNESS_DATA_DIR=~/.claude-channel-hub/data/{botId}/
    │   export DISABLE_OMC=1
    │   export IS_SANDBOX=1
    │
    │ cd ~/.claude-channel-hub/data/{botId}/
    │
    │ claude --dangerously-skip-permissions \
    │        --dangerously-load-development-channels server:telegram-enhanced \
    │        [--continue]  ← 이전 세션이 있을 때만
    │
    ▼
Claude Code Process (tmux 세션 내)
    │
    │ • .mcp.json 읽어 telegram-enhanced MCP 서버 시작
    │ • Telegram 봇 폴링 시작
    │ • Claude Agent Loop 대기
    ▼
Ready

    │ (별도 goroutine)
    │ tmux pipe-pane → /tmp/claude-bot-{botId}.log
    │ tmux send-keys Enter (2/4/6/8초 후, 개발 채널 경고 자동 확인)
```

### 헬스체크 & 재시작

```
Go Harness Supervisor (30초 간격)
    │
    ├── tmux has-session 확인
    │     └── 세션 없으면 → runBot 루프가 재시작 처리
    │
    ├── /tmp/claude-bot-{id}.log 최종 수정 시간 확인
    │     └── 10분 이상 변경 없음 → stale 판정
    │           → Process.Stop() + restartCh 신호
    │
    └── runBot 루프 (exponential backoff)
          restart_delay: 2s → 4s → 8s → ... → 5m (최대)
```

### Graceful Shutdown

```
SIGTERM/SIGINT → Go Harness
    │
    │ 1. 모든 봇 프로세스에 Ctrl-C 전송 (tmux send-keys C-c)
    │ 2. 2초 대기
    │ 3. tmux kill-session
    │ 4. Admin 서버 종료
    ▼
Exit
```

---

## Claude Code 버전 관리

```
~/.claude-channel-hub/
  versions/
    2.1.104/
      node_modules/@anthropic-ai/claude-code/
    2.2.0/
      node_modules/@anthropic-ai/claude-code/
  active → 2.1.104 (symlink)
```

### API

| Method | Path | 동작 |
|---|---|---|
| `GET` | `/api/versions` | 설치된 버전 목록 + 활성 버전 |
| `POST` | `/api/versions/install` | `npm install @anthropic-ai/claude-code@x.y.z` |
| `POST` | `/api/versions/activate` | 활성 버전 전환 → 채널 재시작 |
| `GET` | `/api/versions/check` | npm registry에서 최신 버전 확인 |

---

## 설정 (channels.yaml)

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
  versions_dir: "${HOME}/.claude-channel-hub/versions"
  auto_update: false

defaults:
  plugin: telegram-enhanced
  plugin_dir: ./plugins/telegram-enhanced
  permission_mode: "dangerously-skip"

bots:
  - id: main-bot
    type: telegram
    name: "메인 봇"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: telegram-enhanced
    plugin_dir: ./plugins/telegram-enhanced

channels:
  - id: general
    bot: main-bot
    name: "일반 대화"
    match:
      type: default
    data_dir: "./data/general"

  - id: code-review
    bot: main-bot
    name: "코드리뷰 전용"
    match:
      type: group
      chat_ids: ["-100123456789"]
    model: "claude-sonnet-4-6"
    system_prompt: "코드 리뷰 전문가로서 응답하세요."
    data_dir: "./data/code-review"
```

### 라우팅 규칙 (match)

| type | 설명 | 예시 |
|---|---|---|
| `default` | 다른 채널에 매칭되지 않는 모든 메시지 | DM, 미지정 그룹 |
| `group` | 특정 Telegram 그룹/슈퍼그룹 | `chat_ids: ["-100xxx"]` |
| `user` | 특정 사용자 DM만 | `user_ids: ["12345"]` |
| `topic` | 그룹 내 특정 토픽(포럼) | `chat_ids + topic_ids` |

---

## Go 컴포넌트 구조

```
cmd/bot/main.go                     # 진입점 — 전체 wiring
configs/channels.yaml               # 봇/채널/버전/관리자 설정

internal/
  config/
    config.go                       # YAML 설정 로더 (환경변수 치환)
    dotenv.go                       # .env 파일 로더

  bot/
    bot.go                          # Bot 정의 (id, type, token, plugin, channels)
    process.go                      # tmux 세션 기반 프로세스 관리

  supervisor/
    supervisor.go                   # 봇 프로세스 감시, 자동 재시작, 헬스체크

  version/
    manager.go                      # Claude Code 버전 설치/전환/목록

  admin/
    server.go                       # HTTP 대시보드 + REST API (사이드바 레이아웃)

plugins/
  telegram-enhanced/
    server.ts                       # MCP 서버 — 단일 자기완결 파일 (./src/ import 없음)
    package.json
```

---

## 데이터 경로

```
~/.claude-channel-hub/
  data/
    {botId}/                        # 봇 작업 디렉토리 (HARNESS_DATA_DIR)
      .mcp.json                     # 플러그인 MCP 설정 (plugin_dir에서 자동 복사)
      .claude/sessions/             # Claude 세션 파일 (--continue 대상)
  versions/                         # Claude Code 버전들

~/.claude/channels/
  telegram-{botId}/                 # 봇 상태 (STATE_DIR)
    .env                            봇 토큰
    access.json                     인증/페어링
    bot.pid                         프로세스 ID

/tmp/claude-bot-{botId}.log         # 봇 로그 (tmux pipe-pane)
```

---

## 역할 분리

| 역할 | 담당 | 비고 |
|---|---|---|
| **봇 프로세스 관리** | Go Harness (Supervisor) | 봇 1개 = tmux 세션 1개 |
| **봇/채널 CRUD** | Go Harness (Admin API) | 설정 변경 → 프로세스 재시작 |
| **Claude Code 버전 관리** | Go Harness (Version Manager) | 봇별 버전 지정 가능 |
| **모니터링/대시보드** | Go Harness (Admin Server) | HTTP API + 사이드바 UI |
| **봇 토큰/인증** | Go Harness → TELEGRAM_STATE_DIR/.env | access.json 자동 생성 |
| **채널 라우팅** | telegram-enhanced plugin | HARNESS_CHANNELS_CONFIG 파싱 |
| **메시지 수신/발신** | telegram-enhanced plugin (Grammy) | Telegram Bot API |
| **메모리/프로필** | telegram-enhanced plugin (MCP 도구) | 채널별 data_dir 격리 |
| **AI 응답 생성** | Claude Code (Agent Loop) | 모델, 프롬프트는 채널 설정 |
| **도구 실행** | Claude Code (내장 도구) | Bash, Read, Edit 등 |

---

## Admin REST API

### 봇 관리

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/bots` | 봇 목록 + 프로세스 상태 |
| `POST` | `/api/bots` | 봇 추가 |
| `PUT` | `/api/bots/:id` | 봇 설정 수정 |
| `DELETE` | `/api/bots/:id` | 봇 삭제 (tmux 세션 종료) |
| `POST` | `/api/bots/:id/restart` | 봇 프로세스 재시작 |
| `GET` | `/api/bots/:id/logs` | 봇 로그 (/tmp/claude-bot-{id}.log) |

### 채널 관리

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/channels` | 전체 채널 목록 |
| `GET` | `/api/bots/:id/channels` | 특정 봇의 채널 목록 |
| `POST` | `/api/bots/:id/channels` | 봇에 채널 추가 |
| `PUT` | `/api/channels/:id` | 채널 설정 수정 |
| `DELETE` | `/api/channels/:id` | 채널 삭제 |

### 버전 관리

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/versions` | 설치된 버전 목록 + 활성 버전 |
| `POST` | `/api/versions/install` | 버전 설치 |
| `POST` | `/api/versions/activate` | 기본 버전 전환 |
| `GET` | `/api/versions/check` | 최신 버전 확인 |

### 시스템

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/status` | 전체 시스템 상태 (봇 수, 채널 수, 업타임) |
| `GET` | `/api/events` | 이벤트 로그 (시작, 종료, 에러, 재시작) |
| `GET` | `/api/health` | 헬스체크 |

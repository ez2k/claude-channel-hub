# 아키텍처 — Claude Harness v4

## 개요

Go 프로세스가 Claude Code Channel 프로세스들을 관리하는 **오케스트레이터** 구조.
각 채널(Telegram, Discord 등)은 독립적인 `claude --channels` 프로세스로 실행된다.

---

## 봇과 채널의 관계

```
┌─────── 모드 A: 1봇 = 1채널 (분리) ───────┐
│                                            │
│  Bot (@main_bot)  ──→  Channel: tg-main   │
│  Bot (@beta_bot)  ──→  Channel: tg-beta   │
│                                            │
│  각 봇이 독립 Claude Code 프로세스로 실행    │
│  완전한 격리 (메모리, 프로필, 설정 분리)     │
└────────────────────────────────────────────┘

┌─────── 모드 B: 1봇 = N채널 (통합) ───────┐
│                                            │
│  Bot (@main_bot)  ──┬→  Channel: 공개그룹  │
│                     ├→  Channel: 관리자DM  │
│                     └→  Channel: 스터디방   │
│                                            │
│  하나의 Claude Code 프로세스에서            │
│  chat_id/group 기반으로 채널 라우팅          │
│  채널별 설정 (모델, 프롬프트) 분기 가능      │
│  메모리는 user_id로 격리, 설정은 채널별     │
└────────────────────────────────────────────┘

┌─────── 모드 C: 혼합 ─────────────────────┐
│                                            │
│  Bot (@main_bot)  ──┬→  일반 대화 채널     │
│                     └→  코드리뷰 채널      │
│  Bot (@admin_bot) ──→   관리 전용 채널     │
│                                            │
│  봇 단위로 프로세스 분리                     │
│  봇 내에서 채널 라우팅                      │
└────────────────────────────────────────────┘
```

핵심 개념:
- **Bot**: 플랫폼 봇 (토큰 1개 = 프로세스 1개)
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
│  │  • 봇 단위로 claude 프로세스 생성/종료 (봇 1개 = 프로세스 1개)   │ │
│  │  • 헬스체크 (프로세스 alive + 봇 응답 확인)                      │ │
│  │  • 크래시 감지 → exponential backoff 재시작                      │ │
│  │  • stdout/stderr 로그 캡처                                       │ │
│  │  • Graceful shutdown (SIGTERM → 대기 → SIGKILL)                 │ │
│  └────┬──────────────────┬──────────────────┬──────────────────────┘ │
│       │                  │                  │                        │
│  ┌────┴─────┐      ┌────┴─────┐      ┌────┴─────┐                  │
│  │ Process 1│      │ Process 2│      │ Process N│                  │
│  │ main-bot │      │admin-bot │      │ dc-bot   │                  │
│  └────┬─────┘      └────┬─────┘      └────┬─────┘                  │
│       │                  │                  │                        │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │              Claude Code Version Manager                        │ │
│  │  • 버전 설치/전환/자동 업데이트 • 봇별 버전 지정 가능            │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │              Admin Server (HTTP API + Dashboard)                 │ │
│  │  • REST API: 봇/채널 CRUD, 상태, 로그, 버전 관리                │ │
│  └─────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
         │                  │                  │
         ▼                  ▼                  ▼
┌────────────────┐ ┌────────────────┐ ┌────────────────┐
│ claude          │ │ claude          │ │ claude          │
│ --channels      │ │ --channels      │ │ --channels      │
│ plugin:tg-enh.  │ │ plugin:tg-enh.  │ │ plugin:dc-enh.  │
│                 │ │                 │ │                 │
│ MCP Server:     │ │ MCP Server:     │ │ MCP Server:     │
│ telegram-enh.   │ │ telegram-enh.   │ │ discord-enh.    │
│                 │ │                 │ │                 │
│ Channel Router: │ │ (단일 채널)     │ │ Channel Router: │
│ ┌─────────────┐│ │                 │ │ ┌─────────────┐│
│ │general (DM) ││ │                 │ │ │general      ││
│ │code-review  ││ │                 │ │ │dev-channel  ││
│ │study-group  ││ │                 │ │ └─────────────┘│
│ └─────────────┘│ │                 │ │                 │
└───────┬────────┘ └───────┬────────┘ └───────┬────────┘
        │                  │                  │
        ▼                  ▼                  ▼
    Telegram            Telegram           Discord
   @main_bot           @admin_bot
   ┌────────┐          ┌────────┐
   │공개그룹│          │관리 DM │
   │스터디방│          └────────┘
   │일반 DM │
   └────────┘
```

---

## 프로세스 관리 흐름

### 채널 시작

```
Go Harness
    │
    │ 1. channels.yaml에서 채널 설정 로드
    │ 2. 채널별 플러그인 확인 (설치 여부)
    │ 3. 환경변수 준비 (봇 토큰, 데이터 경로 등)
    ▼
exec.Command()
    │
    │ claude --channels plugin:telegram-enhanced \
    │        --system-prompt "..." \
    │        --model claude-sonnet-4-6 \
    │        --dangerously-skip-permissions \    ← 무인 운영용 (선택)
    │        --no-session-persistence            ← 선택
    │
    │ env: TELEGRAM_BOT_TOKEN=xxx
    │      HARNESS_DATA_DIR=./data/tg-main
    │      HARNESS_CHANNEL_ID=tg-main
    ▼
Claude Code Process
    │
    │ • MCP 서버(telegram-enhanced) 자동 시작
    │ • Grammy 봇 → Telegram 폴링 시작
    │ • Claude Agent Loop 대기
    ▼
Ready (Go Harness가 stdout에서 확인)
```

### 헬스체크 & 재시작

```
Go Harness Supervisor (30초 간격)
    │
    ├── 프로세스 alive 확인 (PID)
    │     └── 죽었으면 → 재시작 (backoff)
    │
    ├── stdout/stderr 모니터링
    │     └── 에러 패턴 감지 → 로그 기록 + 알림
    │
    └── 봇 상태 확인 (선택)
          └── Telegram getMe() 호출 → 타임아웃이면 프로세스 재시작
```

### Graceful Shutdown

```
SIGTERM/SIGINT → Go Harness
    │
    │ 1. 모든 채널 프로세스에 SIGTERM 전송
    │ 2. 최대 10초 대기
    │ 3. 아직 살아있으면 SIGKILL
    │ 4. Admin 서버 종료
    ▼
Exit
```

---

## Claude Code 버전 관리

```
$HOME/.claude-harness/
  versions/
    2.1.104/
      node_modules/@anthropic-ai/claude-code/
    2.2.0/
      node_modules/@anthropic-ai/claude-code/
  active → 2.1.104 (symlink)
```

### 기능

| 명령/API | 동작 |
|---|---|
| `GET /api/versions` | 설치된 버전 목록 + 활성 버전 |
| `POST /api/versions/install` | `npm install @anthropic-ai/claude-code@x.y.z` |
| `POST /api/versions/activate` | 활성 버전 전환 → 채널 재시작 |
| `POST /api/versions/check` | npm registry에서 최신 버전 확인 |
| 자동 업데이트 | cron 체크 → 새 버전 설치 → 채널별 롤링 재시작 |

### 채널별 버전 지정

```yaml
channels:
  - id: tg-main
    type: telegram
    token: "${TELEGRAM_BOT_TOKEN}"
    claude_version: "2.1.104"     # 이 채널은 고정 버전

  - id: tg-beta
    type: telegram
    token: "${TELEGRAM_BETA_TOKEN}"
    claude_version: "latest"       # 항상 최신 버전
```

---

## 설정 (channels.yaml)

```yaml
admin:
  addr: "${ADMIN_ADDR}"

supervisor:
  health_check_interval: 30s
  max_restarts: 10
  restart_delay: 2s
  restart_backoff_max: 5m

claude:
  default_version: "latest"
  versions_dir: "${HOME}/.claude-harness/versions"
  auto_update: true
  auto_update_interval: 24h

defaults:
  model: ""
  system_prompt: ""
  permission_mode: "dangerously-skip"
  plugins_dir: ./plugins

# ─── 봇 정의 (플랫폼 봇 = 프로세스 단위) ───
bots:
  - id: main-bot
    type: telegram
    name: "메인 봇"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: "telegram-enhanced"
    claude_version: ""                    # 비워두면 default_version
    model: ""                             # 봇 기본 모델

  - id: admin-bot
    type: telegram
    name: "관리 봇"
    enabled: true
    token: "${TELEGRAM_ADMIN_TOKEN}"
    plugin: "telegram-enhanced"
    claude_version: "2.1.104"             # 고정 버전
    model: "claude-sonnet-4-6"

  # - id: discord-bot
  #   type: discord
  #   enabled: false
  #   token: "${DISCORD_BOT_TOKEN}"
  #   plugin: "discord-enhanced"

# ─── 채널 정의 (논리적 대화 공간 = 봇 내 라우팅) ───
channels:
  # main-bot 내의 채널들
  - id: general
    bot: main-bot                         # 소속 봇
    name: "일반 대화"
    match:                                # 라우팅 규칙
      type: default                       # 매칭되지 않는 모든 메시지
    model: ""                             # 비워두면 봇 기본값
    system_prompt: ""
    data_dir: "./data/general"

  - id: code-review
    bot: main-bot
    name: "코드리뷰 전용"
    match:
      type: group                         # 특정 그룹 매칭
      chat_ids: ["-100123456789"]         # Telegram group chat ID
    model: "claude-sonnet-4-6"
    system_prompt: "코드 리뷰 전문가로서 응답하세요."
    data_dir: "./data/code-review"

  - id: study-group
    bot: main-bot
    name: "스터디방"
    match:
      type: group
      chat_ids: ["-100987654321"]
    system_prompt: "학습 도우미로서 친절하게 설명하세요."
    data_dir: "./data/study"

  # admin-bot의 채널 (분리된 봇)
  - id: admin-only
    bot: admin-bot
    name: "관리자 전용"
    match:
      type: default
    system_prompt: "시스템 관리 명령만 처리하세요."
    data_dir: "./data/admin"
```

### 라우팅 규칙 (match)

| type | 설명 | 예시 |
|---|---|---|
| `default` | 다른 채널에 매칭되지 않는 모든 메시지 | DM, 미지정 그룹 |
| `group` | 특정 Telegram 그룹/슈퍼그룹 | `chat_ids: ["-100xxx"]` |
| `user` | 특정 사용자 DM만 | `user_ids: ["12345"]` |
| `topic` | 그룹 내 특정 토픽(포럼) | `chat_ids + topic_ids` |

봇 내에서 메시지가 들어오면, channels를 순회하며 match 규칙에 맞는 채널을 찾는다. 매칭된 채널의 설정(model, system_prompt, data_dir)이 적용된다. `default`는 항상 마지막에 매칭된다.

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
    bot.go                          # Bot 정의 (id, type, token, plugin, version)
    manager.go                      # Bot CRUD, 프로세스 할당

  channel/
    channel.go                      # Channel 정의 (id, bot, match, model, prompt, data_dir)
    router.go                       # 채널 라우팅 규칙 (match 타입별)
    manager.go                      # Channel CRUD, 봇에 채널 설정 주입

  supervisor/
    supervisor.go                   # 봇 프로세스 감시, 자동 재시작
    process.go                      # 단일 claude 프로세스 관리 (start/stop/restart)
    health.go                       # 헬스체크 로직

  version/
    manager.go                      # Claude Code 버전 설치/전환/목록
    updater.go                      # 자동 업데이트 체커
    registry.go                     # npm registry 조회

  admin/
    server.go                       # HTTP 대시보드 + REST API
    handlers_bot.go                 # 봇 API 핸들러
    handlers_channel.go             # 채널 API 핸들러
    handlers_version.go             # 버전 API 핸들러

  store/
    store.go                        # 로그/이벤트 영속 저장

plugins/
  telegram-enhanced/                # 포크된 Telegram 채널 플러그인
    server.ts                       # MCP 서버 (공식 기반 + 미들웨어)
    src/
      middleware.ts
      instructions-builder.ts
      middlewares/
        memory-injector.ts
        profile-injector.ts
        command-filter.ts
      tools/
        memory.ts
        profile.ts
        skills.ts
      store/
        memory-store.ts
        profile-store.ts
```

---

## 채널 라우팅 흐름 (1봇 N채널)

```
Telegram 메시지 수신 (Grammy)
    │
    │ 1. gate() — 발신자 인증
    ▼
Middleware Chain
    │
    │ 2. channel-router 미들웨어
    │    chat_id / user_id로 channels 설정 매칭
    │
    │    chat_id=-100123456789 → match: code-review 채널
    │    chat_id=-100987654321 → match: study-group 채널
    │    그 외                 → match: general (default)
    │
    │ 3. 매칭된 채널 설정 적용
    │    ctx.channel = { model, system_prompt, data_dir }
    ▼
memory-injector (채널의 data_dir에서 메모리 로드)
    │
profile-injector (채널의 data_dir에서 프로필 로드)
    │
    ▼
handleInbound()
    │
    │ 4. notification content에 채널 컨텍스트 포함:
    │    - <channel_config model="..." prompt="...">
    │    - <recalled_memories>
    │    - <user_profile>
    ▼
Claude Code → reply()
```

### Go Harness가 플러그인에 전달하는 채널 설정

Go Harness가 claude 프로세스를 시작할 때, 해당 봇에 소속된 채널 설정을 환경변수로 전달:

```bash
claude --channels plugin:telegram-enhanced \
  --system-prompt "기본 시스템 프롬프트"

# 환경변수
TELEGRAM_BOT_TOKEN=xxx
HARNESS_CHANNELS_CONFIG='[
  {"id":"general","match":{"type":"default"},"model":"","system_prompt":"","data_dir":"./data/general"},
  {"id":"code-review","match":{"type":"group","chat_ids":["-100123456789"]},"model":"claude-sonnet-4-6","system_prompt":"코드 리뷰 전문가","data_dir":"./data/code-review"},
  {"id":"study-group","match":{"type":"group","chat_ids":["-100987654321"]},"system_prompt":"학습 도우미","data_dir":"./data/study"}
]'
```

플러그인은 `HARNESS_CHANNELS_CONFIG`를 파싱하여 channel-router 미들웨어에 전달한다.

---

## 역할 분리

| 역할 | 담당 | 비고 |
|---|---|---|
| **봇 프로세스 관리** | Go Harness (Supervisor) | 봇 1개 = 프로세스 1개 |
| **봇/채널 CRUD** | Go Harness (Manager) | 설정 변경 → 프로세스 재시작 |
| **Claude Code 버전 관리** | Go Harness (Version Manager) | 봇별 버전 지정 가능 |
| **모니터링/대시보드** | Go Harness (Admin Server) | HTTP API + UI |
| **봇 토큰/인증** | Go Harness (Config) → Plugin (access.json) | |
| **채널 라우팅** | Channel Plugin (channel-router 미들웨어) | chat_id 기반 분기 |
| **메시지 수신/발신** | Channel Plugin (Grammy) | Telegram/Discord Bot API |
| **메모리/프로필/스킬** | Channel Plugin (MCP 도구 + 미들웨어) | 채널별 data_dir 격리 |
| **AI 응답 생성** | Claude Code (Agent Loop) | 모델, 프롬프트는 채널 설정 |
| **도구 실행** | Claude Code (내장 도구) | Bash, Read, Edit 등 |

---

## 데이터 경로

```
프로젝트 루트/
  data/
    tg-main/                        # 채널별 HARNESS_DATA_DIR
      memory/{user_id}/
      profiles/{user_id}/
      skills/_learned/
      stats/
    dc-main/
      memory/{user_id}/
      ...

~/.claude/channels/telegram/        # STATE_DIR (공식 플러그인 상태)
  .env                               봇 토큰
  access.json                        인증/페어링
  bot.pid                            프로세스 ID

~/.claude-harness/
  versions/                          Claude Code 버전들
    2.1.104/
    2.2.0/
  active -> 2.1.104                  활성 버전 심링크
```

---

## Admin REST API

### 봇 관리

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/bots` | 봇 목록 + 프로세스 상태 |
| `POST` | `/api/bots` | 봇 추가 (토큰, 플러그인, 버전) |
| `PUT` | `/api/bots/:id` | 봇 설정 수정 |
| `DELETE` | `/api/bots/:id` | 봇 삭제 (프로세스 종료) |
| `POST` | `/api/bots/:id/restart` | 봇 프로세스 재시작 |
| `GET` | `/api/bots/:id/logs` | 봇 stdout/stderr 로그 |

### 채널 관리

| Method | Path | 설명 |
|---|---|---|
| `GET` | `/api/channels` | 전체 채널 목록 |
| `GET` | `/api/bots/:id/channels` | 특정 봇의 채널 목록 |
| `POST` | `/api/bots/:id/channels` | 봇에 채널 추가 |
| `PUT` | `/api/channels/:id` | 채널 설정 수정 (모델, 프롬프트, 라우팅) |
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

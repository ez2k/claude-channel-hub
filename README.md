# Claude Channel Hub

Claude Code의 공식 [Channels](https://code.claude.com/docs/ko/channels) 기능을 활용하는 멀티채널 봇 오케스트레이터.
Telegram, Discord 등 여러 봇을 동시에 관리하고, 각 봇의 Claude Code 프로세스를 자동 감시/복구합니다.

## 아키텍처

```
┌──────────────────────────────────────────────────┐
│              Go Harness (Supervisor)              │
│                                                   │
│  Bot Manager ─── Version Manager ─── Admin API   │
└──────┬──────────────────┬──────────────┬─────────┘
       │                  │              │
  ┌────┴─────┐      ┌────┴─────┐   ┌────┴─────┐
  │ claude   │      │ claude   │   │ claude   │
  │ --channels│      │ --channels│   │ --channels│
  │ telegram │      │ discord  │   │ ...      │
  └────┬─────┘      └────┬─────┘   └──────────┘
       │                  │
   Telegram            Discord
   @my_bot             @my_bot
   ┌────────┐          ┌────────┐
   │일반 DM │          │서버    │
   │그룹채팅│          │채널    │
   └────────┘          └────────┘
```

### 핵심 컴포넌트

| 컴포넌트 | 역할 |
|----------|------|
| **Supervisor** | 봇별 Claude Code 프로세스 관리, 헬스체크, 자동 재시작 (exponential backoff) |
| **Bot Manager** | 봇 CRUD, 채널 라우팅 (1봇:N채널 지원) |
| **Version Manager** | Claude Code 버전 설치/전환/봇별 버전 지정 |
| **Channel Plugin** | MCP 서버 기반 메시지 브릿지 (메모리, 프로필, 스킬 확장) |
| **Admin Dashboard** | HTTP 대시보드 + REST API |

### 봇과 채널의 관계

- **Bot** = 플랫폼 봇 토큰 + Claude Code 프로세스 1개
- **Channel** = 논리적 대화 공간 (그룹, DM, 토픽 등)
- 1봇:1채널 (분리), 1봇:N채널 (통합), 혼합 모두 지원

## 빠른 시작

### 사전 요구

- Go 1.22+
- [Claude Code](https://claude.ai/code) (claude.ai 로그인 필요)
- [Bun](https://bun.sh) (채널 플러그인 실행용)
- Telegram 봇 토큰 ([BotFather](https://t.me/BotFather)에서 발급)

### 1. 설정

```bash
git clone https://github.com/ez2k/claude-channel-hub.git
cd claude-channel-hub

cp .env.example .env
# TELEGRAM_BOT_TOKEN 입력
vim .env
```

### 2. Claude Code에 Telegram 플러그인 설치

```bash
claude
# Claude Code 세션에서:
/plugin install telegram@claude-plugins-official
/telegram:configure <YOUR_BOT_TOKEN>
# 봇에 DM 보내고 페어링 코드 승인:
/telegram:access pair <CODE>
/telegram:access policy allowlist
/exit
```

### 3. 빌드 & 실행

```bash
make build
make run
# → 봇 프로세스 시작
# → 대시보드: http://localhost:8082
```

### 4. Docker

```bash
make docker-build
make docker-run
```

## 설정 (channels.yaml)

```yaml
admin:
  addr: "${ADMIN_ADDR}"           # 기본 :8080

claude:
  default_version: "latest"       # Claude Code 버전
  auto_update: false

bots:
  - id: main-bot
    type: telegram
    name: "메인 봇"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: "telegram"
    plugin_marketplace: "claude-plugins-official"

  # - id: discord-bot
  #   type: discord
  #   name: "디스코드 봇"
  #   enabled: false
  #   token: "${DISCORD_BOT_TOKEN}"
  #   plugin: "discord"
  #   plugin_marketplace: "claude-plugins-official"

channels:
  - id: general
    bot: main-bot
    name: "일반 대화"
    match:
      type: default               # 매칭되지 않는 모든 메시지
    data_dir: "./data/general"

  # 특정 그룹에 다른 설정 적용
  # - id: code-review
  #   bot: main-bot
  #   name: "코드리뷰 전용"
  #   match:
  #     type: group
  #     chat_ids: ["-100123456789"]
  #   model: "claude-sonnet-4-6"
  #   system_prompt: "코드 리뷰 전문가로서 응답하세요."
  #   data_dir: "./data/code-review"
```

### 채널 라우팅 규칙

| match.type | 설명 | 예시 |
|---|---|---|
| `default` | 다른 채널에 매칭되지 않는 모든 메시지 | DM, 미지정 그룹 |
| `group` | 특정 Telegram 그룹 | `chat_ids: ["-100xxx"]` |
| `user` | 특정 사용자 DM | `user_ids: ["12345"]` |

## 프로세스 관리

Supervisor가 각 봇을 독립 프로세스로 관리합니다:

- **PTY 할당**: Claude Code가 터미널을 필요로 하므로 pseudo-terminal 생성
- **헬스체크**: 30초마다 프로세스 상태 확인
- **자동 재시작**: 크래시 시 exponential backoff (2s → 4s → 8s → ... → 최대 5분)
- **Graceful Shutdown**: SIGTERM → 10초 대기 → SIGKILL

## Admin 대시보드

`http://localhost:8082` 에서 접근:

- 봇별 상태 (running/stopped/failed)
- 업타임, 재시작 횟수, 채널 수
- 이벤트 타임라인
- 10초 자동 새로고침

### REST API

```
GET  /api/bots                # 봇 목록 + 상태
GET  /api/bots/:id            # 단일 봇 상태
GET  /api/bots/:id/channels   # 봇의 채널 목록
POST /api/bots/:id/restart    # 봇 재시작
GET  /api/channels            # 전체 채널 목록
GET  /api/versions            # Claude Code 버전 목록
GET  /api/status              # 시스템 상태
GET  /api/events              # 이벤트 로그
GET  /api/health              # 헬스체크
```

## telegram-enhanced 플러그인

공식 Telegram 플러그인을 확장한 버전 (`plugins/telegram-enhanced/`):

- **메모리 시스템**: FTS 검색, 유사도 기반 중복 제거, 가중치, 자동 추출
- **사용자 프로필**: 언어 감지 (한/영/일), 스타일 분석, 주제 추적
- **미들웨어 체인**: 채널 라우팅, 커맨드 필터, 메모리/프로필 컨텍스트 주입
- **MCP 도구**: `memory_recall`, `memory_save`, `memory_stats`, `profile_get`, `profile_update`

## 데이터 마이그레이션

Go 하네스 v2 데이터를 v4로 마이그레이션:

```bash
bun scripts/migrate.ts --from ./data --to ~/.claude-channel-hub/data
```

## 프로젝트 구조

```
claude-channel-hub/
├── cmd/bot/main.go                 # 진입점
├── configs/channels.yaml           # 봇/채널 설정
├── internal/
│   ├── admin/server.go             # HTTP 대시보드 + REST API
│   ├── bot/
│   │   ├── bot.go                  # Bot 정의
│   │   └── process.go              # Claude Code 프로세스 관리 (PTY)
│   ├── config/config.go            # YAML 설정 로더
│   ├── supervisor/supervisor.go    # 봇 프로세스 감시
│   └── version/manager.go          # Claude Code 버전 관리
├── plugins/
│   └── telegram-enhanced/          # 확장 Telegram 플러그인
│       ├── server.ts               # MCP 서버 (공식 기반)
│       └── src/
│           ├── middleware.ts        # 미들웨어 체인
│           ├── channel-router.ts   # 채널 라우팅
│           ├── store/
│           │   ├── memory-store.ts # 메모리 시스템
│           │   └── profile-store.ts# 사용자 프로필
│           └── tools/
│               ├── memory-tools.ts # 메모리 MCP 도구
│               └── profile-tools.ts# 프로필 MCP 도구
├── scripts/migrate.ts              # 데이터 마이그레이션
├── docs/
│   ├── ARCHITECTURE.md             # 상세 아키텍처
│   └── DISCORD_SETUP.md            # Discord 설정 가이드
├── skills/                         # 스킬 디렉토리
├── Dockerfile
└── Makefile
```

## Discord 설정

[Discord 설정 가이드](docs/DISCORD_SETUP.md)를 참고하세요.

## 라이선스

MIT

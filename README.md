# Claude Channel Hub

Claude Code의 공식 [Channels](https://code.claude.com/docs/ko/channels) 기능을 활용하는 멀티채널 봇 오케스트레이터.
Telegram 등 여러 봇을 동시에 관리하고, 각 봇의 Claude Code 프로세스를 자동 감시/복구합니다.

## 아키텍처

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
   │일반 DM │          │그룹채팅│
   │그룹채팅│          └────────┘
   └────────┘
```

### 핵심 컴포넌트

| 컴포넌트 | 역할 |
|----------|------|
| **Supervisor** | 봇별 tmux 세션 관리, 헬스체크, 자동 재시작 (exponential backoff) |
| **Bot Manager** | 봇 CRUD, 채널 라우팅 (1봇:N채널 지원) |
| **Version Manager** | Claude Code 버전 설치/전환/봇별 버전 지정 |
| **Channel Plugin** | MCP 서버 기반 메시지 브릿지 (메모리, 프로필, 스킬 확장) |
| **Admin Dashboard** | HTTP 대시보드 + REST API |

### 봇과 채널의 관계

- **Bot** = 플랫폼 봇 토큰 + tmux 세션 1개 (claude 프로세스)
- **Channel** = 논리적 대화 공간 (그룹, DM, 토픽 등)
- 1봇:1채널 (분리), 1봇:N채널 (통합), 혼합 모두 지원

## 빠른 시작

### 사전 요구

- Go 1.22+
- [Claude Code](https://claude.ai/code) (claude.ai 로그인 필요)
- [Bun](https://bun.sh) (채널 플러그인 실행용)
- tmux (봇 프로세스 세션 관리용)
- Telegram 봇 토큰 ([BotFather](https://t.me/BotFather)에서 발급)

### 1. 설정

```bash
git clone https://github.com/ez2k/claude-channel-hub.git
cd claude-channel-hub

cp .env.example .env
# TELEGRAM_BOT_TOKEN 입력
vim .env
```

### 2. 빌드 & 실행

```bash
make build
make run
# → 봇 프로세스 시작 (tmux 세션: cch-{botId})
# → 대시보드: http://localhost:8082
```

### 3. 설치 스크립트 (systemd 서비스 포함)

```bash
sudo bash install.sh
sudo systemctl start claude-channel-hub
```

### 4. Docker

```bash
make docker-build
make docker-run
```

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
  auto_update: false

defaults:
  plugin: telegram-enhanced
  plugin_dir: ./plugins/telegram-enhanced

bots:
  - id: main-bot
    type: telegram
    name: "메인 봇"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    plugin: telegram-enhanced
    plugin_dir: ./plugins/telegram-enhanced

  # - id: second-bot
  #   type: telegram
  #   name: "두번째 봇"
  #   enabled: false
  #   token: "${TELEGRAM_SECOND_TOKEN}"
  #   plugin: telegram-enhanced
  #   plugin_dir: ./plugins/telegram-enhanced

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

Supervisor가 각 봇을 tmux 세션으로 관리합니다:

- **tmux 세션**: 봇 ID별 세션 `cch-{botId}` 생성, 독립 실행
- **채널 모드**: `--dangerously-load-development-channels server:telegram-enhanced`
- **작업 디렉토리**: `~/.claude-channel-hub/data/{botId}/` (봇별 격리)
- **봇 상태**: `~/.claude/channels/telegram-{botId}/` (access.json, .env)
- **헬스체크**: 30초마다 tmux 세션 상태 + 10분 idle 감지 → 자동 재시작
- **자동 재시작**: 크래시 시 exponential backoff (2s → 4s → 8s → ... → 최대 5분)
- **프롬프트 자동응답**: tmux send-keys Enter (개발 채널 경고 자동 확인)
- **좀비 프로세스 정리**: 동일 토큰 bun 프로세스 자동 kill (Telegram 409 방지)
- **로그**: `/tmp/claude-bot-{botId}.log` (tmux pipe-pane)
- **세션 재개**: 이전 세션이 있으면 `--continue` 플래그 자동 적용

## Admin 대시보드

`http://localhost:8082` 에서 접근:

- 사이드바 레이아웃 (봇 목록 + 상세 패널)
- 봇별 상태 (running/stopped/failed), 업타임, 재시작 횟수
- 봇 CRUD (추가/수정/삭제)
- 접근 관리 모달 (access.json 편집)
- 메모리 뷰어
- tmux 세션 이름 표시

### REST API

```
GET  /api/bots                # 봇 목록 + 상태
GET  /api/bots/:id            # 단일 봇 상태
POST /api/bots                # 봇 추가
PUT  /api/bots/:id            # 봇 수정
DELETE /api/bots/:id          # 봇 삭제
GET  /api/bots/:id/channels   # 봇의 채널 목록
POST /api/bots/:id/restart    # 봇 재시작
GET  /api/bots/:id/logs       # 봇 로그 (/tmp/claude-bot-{id}.log)
GET  /api/channels            # 전체 채널 목록
GET  /api/versions            # Claude Code 버전 목록
GET  /api/status              # 시스템 상태
GET  /api/events              # 이벤트 로그
GET  /api/health              # 헬스체크
```

## telegram-enhanced 플러그인

`plugins/telegram-enhanced/server.ts` — 단일 자기완결 파일 (외부 ./src/ import 없음):

- **메모리 시스템**: FTS 검색, 유사도 기반 중복 제거, 가중치, 자동 추출
- **사용자 프로필**: 언어 감지 (한/영/일), 스타일 분석, 주제 추적
- **MCP 도구**: `memory_recall`, `memory_save`, `memory_stats`, `profile_get`, `profile_update`
- **채널 라우팅**: `HARNESS_CHANNELS_CONFIG` 환경변수로 채널별 설정 적용

## 데이터 경로

```
~/.claude-channel-hub/
  data/
    {botId}/                    # 봇 작업 디렉토리 (HARNESS_DATA_DIR)
      .mcp.json                 # 플러그인 MCP 설정 (자동 복사)
      .claude/sessions/         # Claude 세션 파일

~/.claude/channels/
  telegram-{botId}/             # 봇 상태 디렉토리
    access.json                 # 인증/페어링
    bot.pid                     # 프로세스 ID
    .env                        # 봇 토큰

/tmp/claude-bot-{botId}.log     # 봇 로그 (tmux pipe-pane)
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
│   │   └── process.go              # tmux 세션 기반 프로세스 관리
│   ├── config/config.go            # YAML 설정 로더
│   ├── supervisor/supervisor.go    # 봇 프로세스 감시
│   └── version/manager.go         # Claude Code 버전 관리
├── plugins/
│   └── telegram-enhanced/          # Telegram 채널 플러그인
│       ├── server.ts               # MCP 서버 (단일 파일, 자기완결)
│       └── package.json
├── docs/
│   └── ARCHITECTURE.md             # 상세 아키텍처
├── claude-channel-hub.service      # systemd 서비스
├── install.sh                      # 설치 스크립트
├── Dockerfile
└── Makefile
```

## 라이선스

MIT

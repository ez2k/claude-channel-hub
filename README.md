# 🤖 Claude Harness v2 — Multi-Channel Agent Supervisor

Claude API 기반 멀티채널 에이전트 하네스.
여러 채널(텔레그램, Discord, Slack 등)을 동시에 관리하고,
각 채널의 Claude 프로세스를 자동 감시/복구합니다.

## 아키텍처

```
                    ┌──────────────┐
                    │  Admin HTTP  │ ← localhost:8080
                    │  Dashboard   │   (상태 모니터링, 채널 제어)
                    └──────┬───────┘
                           │
                    ┌──────┴───────┐
                    │  Supervisor  │ ← 프로세스 감시, 자동 재시작
                    └──────┬───────┘
                           │
          ┌────────────────┼────────────────┐
          │                │                │
   ┌──────┴──────┐  ┌─────┴──────┐  ┌──────┴──────┐
   │  Telegram   │  │  Discord   │  │   Slack     │
   │  Channel    │  │  Channel   │  │  Channel    │
   └──────┬──────┘  └─────┬──────┘  └──────┬──────┘
          │                │                │
   ┌──────┴──────────────────────────────────┐
   │              Agent Loop                  │
   │  (Claude API + Tools + Skills)          │
   └─────────────────────────────────────────┘
```

### 핵심 컴포넌트

| 컴포넌트 | 역할 |
|----------|------|
| **Supervisor** | 채널별 고루틴 관리, 헬스체크, 자동 재시작 (exponential backoff) |
| **Channel** | 플랫폼별 인터페이스 구현 (Telegram 완성, Discord/Slack 예정) |
| **Agent** | Claude API 에이전트 루프 (도구 호출 → 결과 피드백 → 반복) |
| **Skills** | Claude Code 스타일 SKILL.md 로딩, 텔레그램 메뉴 자동 등록 |
| **Admin** | HTTP 대시보드 + REST API로 채널 관리 |

## 빠른 시작

### 1. 설정

```bash
cp .env.example .env
# ANTHROPIC_API_KEY, TELEGRAM_BOT_TOKEN 입력
vim configs/channels.yaml   # 채널 설정
```

### 2. 실행

```bash
make run
# → 텔레그램 봇 시작
# → 대시보드: http://localhost:8080
```

### 3. Docker

```bash
make docker-build
make docker-run
```

## 채널 설정 (channels.yaml)

```yaml
admin:
  addr: ":8080"

supervisor:
  health_check_interval: 30s
  max_restarts: 10

channels:
  - id: tg-main
    type: telegram
    name: "메인 봇"
    enabled: true
    token: "${TELEGRAM_BOT_TOKEN}"
    model: claude-sonnet-4-6-20250514
    skills_dir: ./skills

  - id: tg-code
    type: telegram
    name: "코딩 전용 봇"
    enabled: true
    token: "${TELEGRAM_CODE_BOT_TOKEN}"
    model: claude-sonnet-4-6-20250514
    skills_dir: ./skills/coding
```

- 채널별 독립 봇 토큰, 모델, 스킬 디렉토리 설정 가능
- `${ENV_VAR}` 환경변수 참조 지원
- 채널 활성화/비활성화: `enabled: true/false`

## 프로세스 감시

Supervisor가 각 채널을 독립 고루틴으로 실행하고 감시합니다:

- **헬스체크**: 30초마다 각 채널 플랫폼에 Ping
- **자동 재시작**: 채널 크래시 시 exponential backoff로 재시작 (최대 10회)
- **이벤트 로그**: 시작/정지/에러/재시작 이력 1000건 보관

## Admin 대시보드

`http://localhost:8080` 에서 접근:

- 채널별 상태 (running/stopped/error)
- 업타임, 재시작 횟수, 세션 수, 메시지 수
- Start / Stop / Restart 버튼
- 최근 이벤트 타임라인

### REST API

```
GET  /api/channels              # 전체 채널 상태
GET  /api/channels/{id}         # 단일 채널 상태
POST /api/channels/{id}/start   # 채널 시작
POST /api/channels/{id}/stop    # 채널 정지
POST /api/channels/{id}/restart # 채널 재시작
GET  /api/events                # 이벤트 로그
GET  /api/health                # 전체 헬스 상태
```

## 스킬 시스템

Claude Code와 동일한 `SKILL.md` 형식:

```
skills/
├── code_review/SKILL.md
├── shell/SKILL.md
└── research/SKILL.md
```

스킬이 로드되면 텔레그램 메뉴에 자동 등록됩니다.

```bash
# 새 스킬 추가
make add-skill NAME=translate DESC="다국어 번역"
```

## 프로젝트 구조

```
claude-harness/
├── cmd/bot/main.go                 # 진입점 — 전체 wiring
├── configs/channels.yaml           # 채널 설정
├── internal/
│   ├── admin/server.go             # HTTP 대시보드 + REST API
│   ├── agent/agent.go              # Claude API 에이전트 루프
│   ├── channel/
│   │   ├── channel.go              # Channel 인터페이스 + Metrics
│   │   └── telegram.go             # Telegram 구현체
│   ├── config/config.go            # YAML 설정 로더
│   ├── skills/registry.go          # 스킬 로더
│   ├── supervisor/supervisor.go    # 프로세스 관리자
│   └── tools/tools.go              # 도구 정의
├── skills/                         # 스킬 디렉토리
├── Dockerfile
├── Makefile
└── .env.example
```

## 새 채널 플랫폼 추가하기

`channel.Channel` 인터페이스를 구현하면 됩니다:

```go
type Channel interface {
    ID() string
    Type() string
    Name() string
    Start(ctx context.Context) error
    Stop() error
    Ping() error
}
```

1. `internal/channel/discord.go` 생성
2. `Channel` 인터페이스 구현
3. `cmd/bot/main.go`의 switch에 `case "discord":` 추가

## 비용 관리

- API 직접 사용 → 토큰 단위 과금
- Console에서 **auto-reload 비활성화** + **spend limit 설정** 권장
- `MaxIterations` (기본 15)로 에이전트 루프 제한
- 채널별 모델 설정으로 비용 최적화 가능 (Haiku ← 가벼운 작업)

## 라이선스

MIT

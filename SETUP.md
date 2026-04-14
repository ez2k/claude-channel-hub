# Claude Channel Hub — 설치 & 설정 가이드

## 사전 요구사항

- Go 1.22+
- [Claude Code](https://claude.ai/code) — `claude` CLI (claude.ai 로그인 필요)
- [Bun](https://bun.sh) — 채널 플러그인 실행용
- tmux — 봇 프로세스 세션 관리용
- Telegram 봇 토큰 ([BotFather](https://t.me/BotFather)에서 발급)

## 1단계: 프로젝트 클론

```bash
git clone https://github.com/ez2k/claude-channel-hub.git
cd claude-channel-hub
```

## 2단계: 의존성 & 빌드

```bash
# Go 의존성
go mod tidy

# 바이너리 빌드
go build -o bin/claude-channel-hub ./cmd/bot/
# 또는:
make build
```

## 3단계: 환경 설정

```bash
cp .env.example .env
```

`.env` 파일 편집:
```env
TELEGRAM_BOT_TOKEN=여기에_봇_토큰
# TELEGRAM_SECOND_BOT_TOKEN=두번째_봇_토큰  # 선택
```

> `ANTHROPIC_API_KEY`는 불필요합니다 — Claude Code는 claude.ai 구독 인증을 사용하며,
> 하네스가 봇 프로세스 시작 시 자동으로 이 키를 환경에서 제거합니다.

## 4단계: channels.yaml 설정

`configs/channels.yaml` 편집:

```yaml
admin:
  addr: ":8082"

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
```

## 5단계: 플러그인 의존성 설치

```bash
cd plugins/telegram-enhanced
bun install
cd ../..
```

## 6단계: 실행

### 직접 실행

```bash
make run
# → 봇 프로세스 시작 (tmux 세션: cch-{botId})
# → 대시보드: http://localhost:8082
```

### systemd 서비스로 실행

```bash
sudo bash install.sh

# 서비스 시작
sudo systemctl start claude-channel-hub
sudo systemctl enable claude-channel-hub  # 부팅 시 자동 시작

# 상태 확인
sudo systemctl status claude-channel-hub
journalctl -u claude-channel-hub -f
```

## 봇 프로세스 확인

```bash
# tmux 세션 목록 (봇별 세션: cch-{botId})
tmux ls

# 특정 봇 세션 연결 (대화형 확인)
tmux attach -t cch-main-bot

# 봇 로그 확인
tail -f /tmp/claude-bot-main-bot.log
```

## 대시보드

`http://localhost:8082` 에서:

- 봇 상태 확인 (running/stopped/failed)
- 봇 추가/수정/삭제 (CRUD)
- 봇 재시작
- 접근 관리 (access.json — Telegram 페어링)
- 메모리 뷰어
- 로그 확인

## 문제 해결

### 봇이 시작되지 않는 경우

```bash
# 로그 확인
tail -50 /tmp/claude-bot-{botId}.log

# tmux 세션 직접 확인
tmux attach -t cch-{botId}
```

### Telegram 409 Conflict 오류

동일 토큰으로 여러 프로세스가 폴링 중인 경우. 하네스가 자동으로 정리하지만
수동으로 처리하려면:

```bash
# 기존 봇 프로세스 종료
tmux kill-session -t cch-{botId}
# bun 프로세스 확인
ps aux | grep bun
```

### 개발 채널 경고가 나타나는 경우

하네스가 tmux send-keys Enter로 자동 응답합니다 (시작 후 2/4/6/8초).
수동으로 확인하려면:

```bash
tmux attach -t cch-{botId}
# Enter 키 입력 후 Ctrl-B D로 분리
```

## Claude Code 로그인 확인

봇 프로세스는 시스템에 로그인된 Claude Code 인증을 사용합니다.
인증이 만료되면 봇이 응답하지 않습니다:

```bash
claude  # 로그인 상태 확인
```

## 프로젝트 구조 빠른 참조

```
configs/channels.yaml             ← 봇/채널 설정
internal/bot/process.go           ← tmux 세션 기반 프로세스 관리
internal/supervisor/supervisor.go ← 봇 감시 + 자동 재시작
internal/admin/server.go          ← 대시보드 + REST API
plugins/telegram-enhanced/server.ts ← MCP 채널 플러그인 (단일 파일)
CLAUDE.md                         ← Claude Code 프로젝트 컨텍스트
```

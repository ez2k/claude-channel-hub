# Claude Channel Hub — Project Context

## 프로젝트 개요
Go 기반 멀티봇 오케스트레이터. Claude Code의 공식 Channels 기능을 활용해
Telegram 봇들을 tmux 세션으로 관리하며 자동 감시/복구합니다.

## 아키텍처
```
Telegram ←→ telegram-enhanced plugin (MCP) ←→ Claude Code
                     ↕
          HARNESS_CHANNELS_CONFIG (채널 라우팅)
          HARNESS_DATA_DIR (~/.claude-channel-hub/data/{botId}/)
```

### 프로세스 관리 방식
- **tmux 세션**: 봇 1개 = tmux 세션 1개 (`cch-{botId}`)
- **채널 시작 명령**: `claude --dangerously-skip-permissions --dangerously-load-development-channels server:telegram-enhanced`
- **세션 재개**: 이전 세션이 있으면 `--continue` 추가
- **환경변수**: `DISABLE_OMC=1`, `IS_SANDBOX=1`, `ANTHROPIC_API_KEY` 제거
- **프롬프트 자동응답**: tmux send-keys Enter (개발 채널 경고 확인)
- **좀비 정리**: killStaleBotProcesses — 동일 토큰 bun 프로세스 kill
- **헬스체크**: 30초 간격, 10분 idle → 자동 재시작
- **로그**: `/tmp/claude-bot-{botId}.log` (tmux pipe-pane)

## 디렉토리 구조
```
cmd/bot/main.go                  # 진입점 — 전체 wiring
configs/channels.yaml            # 봇/채널 설정
internal/
  bot/
    bot.go                       # Bot 정의 (config, channels, process)
    process.go                   # tmux 세션 기반 프로세스 관리
  supervisor/supervisor.go       # 봇 감시, exponential backoff 재시작
  admin/server.go                # HTTP 대시보드 + REST API (사이드바 레이아웃)
  config/config.go               # YAML 설정 로더 (환경변수 치환)
  version/manager.go             # Claude Code 버전 관리
plugins/
  telegram-enhanced/
    server.ts                    # MCP 서버 (단일 파일, ./src/ import 없음)
    package.json
```

## 데이터 경로
```
~/.claude-channel-hub/data/{botId}/   # 봇 작업 디렉토리 (HARNESS_DATA_DIR)
  .mcp.json                           # 플러그인 MCP 설정 (자동 복사)
  .claude/sessions/                   # Claude 세션

~/.claude/channels/telegram-{botId}/ # 봇 상태
  access.json                         # 인증/페어링
  bot.pid
  .env                                # 봇 토큰

/tmp/claude-bot-{botId}.log           # 봇 로그
```

## 기술 스택
- Go 1.22+, module: `github.com/ez2k/claude-channel-hub`
- `gopkg.in/yaml.v3` — 설정 파싱
- tmux — 봇 프로세스 세션 관리
- Bun + TypeScript — telegram-enhanced MCP 플러그인

## 빌드 & 실행
```bash
go build -o bin/claude-channel-hub ./cmd/bot/
./bin/claude-channel-hub -config configs/channels.yaml
```

## 테스트
```bash
go test ./...
```

## 주요 설계 결정
1. **tmux 세션**: PTY 대신 tmux로 Claude Code 프로세스 실행. 세션 이름 `cch-{botId}`.
2. **개발 채널 모드**: `--dangerously-load-development-channels server:telegram-enhanced` + 봇 작업 디렉토리의 `.mcp.json`.
3. **단일 파일 플러그인**: `plugins/telegram-enhanced/server.ts` — 외부 ./src/ import 없이 자기완결.
4. **환경 격리**: `DISABLE_OMC=1`, `IS_SANDBOX=1`, `ANTHROPIC_API_KEY` 필터링.
5. **Supervisor**: exponential backoff 재시작, 10분 idle 감지 → 자동 재시작.
6. **Admin 대시보드**: 사이드바 레이아웃, 봇 CRUD, 접근 관리 모달, 메모리 뷰어.

## 다음 작업 후보
- [ ] Discord 채널 구현
- [ ] CLI 모드에서 stream-json 지원 (실시간 스트리밍)
- [ ] 대시보드 실시간 로그 스트리밍
- [ ] 사용량 모니터링 (토큰 카운트, 비용 추정)
- [ ] 테스트 커버리지 확대

## 코딩 컨벤션
- 에러는 `fmt.Errorf("context: %w", err)` 로 래핑
- 로그 이모지: ✅ 성공, ❌ 에러, ⚠️ 경고, 🔧 도구, 🧠 메모리, 📦 설치
- 봇별 로그 접두사: `[botId]`
- 외부 패키지 최소화 (stdlib 우선)

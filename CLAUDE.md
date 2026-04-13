# Claude Channel Hub — Project Context

## 프로젝트 개요
Claude API 기반 셀프-임프루빙 멀티채널 에이전트 하네스. Go로 작성.
텔레그램 봇을 통해 Claude와 대화하며, Hermes Agent 스타일의 자기학습 기능 내장.

## 아키텍처
```
Telegram ←→ Channel ←→ Agent ←→ Provider (CLI or API) ←→ Claude
                         ↕
              Memory + Profile + Skills + Tools
```

### Provider 이중 모드
- **CLI 모드** (`provider: cli`): `claude -p` 서브프로세스 호출. 구독 인증 사용. 세션 기록 없음.
- **API 모드** (`provider: api`): Anthropic SDK 직접 호출. API 키 필요. 토큰 과금.

## 디렉토리 구조
```
cmd/bot/main.go                  # 진입점 — 전체 wiring
configs/channels.yaml            # 채널/프로바이더 설정
internal/
  provider/
    provider.go                  # Provider 인터페이스
    cli.go                       # claude -p 서브프로세스 (구독 모드)
    api.go                       # Anthropic SDK 직접 호출 (API 모드)
  agent/agent.go                 # 에이전트 루프 (메모리+프로필+스킬진화 통합)
  channel/
    channel.go                   # Channel 인터페이스 + Metrics
    telegram.go                  # Telegram 구현 (명령어, 세션, 마켓플레이스)
  supervisor/supervisor.go       # 멀티채널 프로세스 감시, 자동 재시작
  admin/server.go                # HTTP 대시보드 + REST API
  memory/memory.go               # 영속 메모리 (FTS 검색, 자동 추출)
  profile/profile.go             # 사용자 프로파일 모델링
  skills/
    registry.go                  # SKILL.md 로더
    evolve.go                    # 스킬 자동 생성/개선
    marketplace.go               # skills.sh 검색 + GitHub 설치
  store/store.go                 # 대화 영속 저장 (채널별 분리)
  tools/tools.go                 # 내장 도구 (bash, file I/O, web_fetch)
  config/config.go               # YAML 설정 로더 (환경변수 치환)
skills/                          # 스킬 디렉토리
  code_review/SKILL.md
  shell/SKILL.md
  research/SKILL.md
  _learned/                      # 자동 생성된 스킬
```

## 기술 스택
- Go 1.22+
- `github.com/anthropics/anthropic-sdk-go` — Anthropic API (API 모드)
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` — Telegram
- `gopkg.in/yaml.v3` — 설정 파싱

## 빌드 & 실행
```bash
go build -o bin/claude-channel-hub ./cmd/bot/
./bin/claude-channel-hub -config configs/channels.yaml -data ./data
```

## 테스트
```bash
go test -v ./cmd/bot/     # 유닛 테스트 (memory, profile, skills, store, marketplace)
go test ./...             # 전체
```

## 주요 설계 결정
1. **Provider 추상화**: CLI/API 모드를 동일한 인터페이스로. Agent는 Provider만 알면 됨.
2. **세션 기록 없음**: CLI 모드(`-p`)는 stateless. 대화 저장은 하네스의 `store` 패키지가 담당.
3. **Hermes 기능**: 메모리(자동 추출+검색), 프로필(언어/주제/스타일 감지), 스킬 자기진화.
4. **Supervisor**: 각 채널을 고루틴으로 실행, 크래시 시 exponential backoff 재시작.
5. **스킬 마켓**: skills.sh API로 검색, GitHub에서 SKILL.md 다운로드 설치.

## 다음 작업 후보
- [ ] Discord 채널 구현 (`internal/channel/discord.go`)
- [ ] Slack 채널 구현 (`internal/channel/slack.go`)
- [ ] CLI 모드에서 stream-json 지원 (실시간 스트리밍)
- [ ] MCP 서버 연동
- [ ] 대시보드에 메모리/프로필 브라우저 추가
- [ ] 스킬 마켓 설치 시 의존성 자동 해결
- [ ] 사용량 모니터링 (토큰 카운트, 비용 추정)
- [ ] 테스트 커버리지 확대

## 코딩 컨벤션
- 에러는 `fmt.Errorf("context: %w", err)` 로 래핑
- 로그 이모지: ✅ 성공, ❌ 에러, ⚠️ 경고, 🔧 도구, 🧠 메모리, 📦 설치
- 채널별 로그 접두사: `[channel-id]`
- 외부 패키지 최소화 (stdlib 우선)

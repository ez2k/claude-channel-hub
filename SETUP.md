# 🚀 Claude Code에서 이어서 작업하기

## 1단계: 프로젝트 다운로드 & 압축 해제

```bash
# 다운로드한 zip 파일 압축 해제
unzip claude-harness.zip
cd claude-harness
```

## 2단계: Go 모듈 초기화

```bash
# 모듈 이름을 본인 레포로 변경 (선택)
# go.mod의 module 경로를 원하는 이름으로 수정
# 예: github.com/myname/claude-harness

# 의존성 다운로드
go mod tidy
```

## 3단계: 빌드 확인

```bash
go build ./cmd/bot/
# 에러 없으면 성공
```

## 4단계: 환경 설정

```bash
cp .env.example .env
```

`.env` 파일 편집:
```env
# CLI 모드면 이건 안 넣어도 됨
# ANTHROPIC_API_KEY=sk-ant-api03-xxxxx

# 텔레그램 봇 토큰 (BotFather에서 발급)
TELEGRAM_BOT_TOKEN=여기에_토큰
```

`configs/channels.yaml`에서 provider 모드 확인:
```yaml
defaults:
  provider: cli    # 구독 사용 (API 키 불필요)
  # provider: api  # API 키 필요
```

## 5단계: Claude Code에서 열기

```bash
cd claude-harness
claude
```

Claude Code가 `CLAUDE.md`를 자동으로 읽고 프로젝트 컨텍스트를 파악합니다.

## 6단계: 작업 시작

Claude Code에서 바로 쓸 수 있는 프롬프트 예시:

### 기능 추가
```
Discord 채널을 구현해줘. internal/channel/discord.go에
Channel 인터페이스를 구현하고 main.go에 연결해.
```

### 버그 수정
```
internal/provider/cli.go에서 멀티턴 대화가 
claude -p에 제대로 전달되는지 확인하고 개선해줘.
```

### 테스트
```
go test -v ./cmd/bot/ 실행해서 모든 테스트 통과하는지 확인해줘.
```

### 스킬 추가
```
make add-skill NAME=git_helper DESC="Git 워크플로우 자동화"
내용은 커밋 메시지 작성, PR 리뷰, 브랜치 전략을 도와주는 스킬로 작성해줘.
```

### 실행
```
make run 으로 실행해보고, 텔레그램에서 테스트해줘.
```

## 프로젝트 구조 빠른 참조

```
중요한 파일만:

configs/channels.yaml     ← 채널 설정 (여기서 provider 모드 변경)
internal/provider/cli.go  ← CLI 모드 핵심 (claude -p 호출)
internal/agent/agent.go   ← 에이전트 루프 핵심
internal/channel/telegram.go ← 텔레그램 봇 전체 로직
CLAUDE.md                 ← Claude Code가 읽는 프로젝트 컨텍스트
```

## 유용한 Claude Code 명령어

```bash
# 프로젝트 구조 파악
claude "이 프로젝트의 아키텍처를 설명해줘"

# 특정 파일 수정
claude "internal/provider/cli.go에서 스트리밍 응답 지원 추가해줘"

# 전체 빌드+테스트
claude "go build ./cmd/bot/ && go test ./... 실행해줘"

# Git 커밋
claude "변경사항 확인하고 적절한 커밋 메시지로 커밋해줘"
```

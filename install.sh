#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVICE_NAME="claude-channel-hub"

echo "╔══════════════════════════════════════════╗"
echo "║   Claude Channel Hub — 설치 스크립트      ║"
echo "╚══════════════════════════════════════════╝"
echo ""

# 1. 사전 요구사항 확인
echo "📋 사전 요구사항 확인..."
command -v go >/dev/null 2>&1 || { echo "❌ Go가 필요합니다. https://go.dev/dl/"; exit 1; }
command -v bun >/dev/null 2>&1 || { echo "⚠️  Bun 설치 중..."; npm install -g bun; }
command -v claude >/dev/null 2>&1 || { echo "❌ Claude Code가 필요합니다. npm install -g @anthropic-ai/claude-code"; exit 1; }
command -v tmux >/dev/null 2>&1 || { echo "❌ tmux가 필요합니다. apt install tmux 또는 brew install tmux"; exit 1; }
echo "  ✅ Go $(go version | grep -oP 'go[\d.]+')"
echo "  ✅ Bun $(bun --version)"
echo "  ✅ Claude Code $(claude --version 2>/dev/null | head -1)"
echo "  ✅ tmux $(tmux -V)"
echo ""

# 2. Go 빌드
echo "🔨 빌드 중..."
cd "$SCRIPT_DIR"
go build -o bin/claude-channel-hub ./cmd/bot/
echo "  ✅ bin/claude-channel-hub"
echo ""

# 3. .env 확인
if [ ! -f "$SCRIPT_DIR/.env" ]; then
  cp "$SCRIPT_DIR/.env.example" "$SCRIPT_DIR/.env"
  echo "⚠️  .env 파일이 생성되었습니다. 토큰을 설정하세요:"
  echo "  vim $SCRIPT_DIR/.env"
  echo ""
fi

# 4. 플러그인 의존성 설치
echo "📦 telegram-enhanced 플러그인 의존성 설치..."
PLUGIN_DIR="$SCRIPT_DIR/plugins/telegram-enhanced"
if [ -f "$PLUGIN_DIR/package.json" ]; then
  cd "$PLUGIN_DIR" && bun install --no-summary 2>/dev/null
  cd "$SCRIPT_DIR"
  echo "  ✅ 플러그인 의존성 설치 완료"
else
  echo "  ⚠️  plugins/telegram-enhanced/package.json 없음"
fi
echo ""

# 5. systemd 서비스 설치
echo "🔧 systemd 서비스 설치..."
cp "$SCRIPT_DIR/claude-channel-hub.service" /etc/systemd/system/
systemctl daemon-reload
echo "  ✅ 서비스 등록 완료"
echo ""

# 6. 안내
echo "╔══════════════════════════════════════════╗"
echo "║   설치 완료!                              ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "서비스 관리:"
echo "  시작:    sudo systemctl start $SERVICE_NAME"
echo "  중지:    sudo systemctl stop $SERVICE_NAME"
echo "  재시작:  sudo systemctl restart $SERVICE_NAME"
echo "  상태:    sudo systemctl status $SERVICE_NAME"
echo "  로그:    journalctl -u $SERVICE_NAME -f"
echo "  부팅시:  sudo systemctl enable $SERVICE_NAME"
echo ""
echo "대시보드: http://localhost:8082"
echo ""
echo "tmux 세션 확인 (봇별):"
echo "  tmux ls"
echo "  tmux attach -t cch-{botId}"
echo ""
echo "다음 단계:"
echo "  1. .env에 TELEGRAM_BOT_TOKEN 설정"
echo "  2. configs/channels.yaml에 봇/채널 설정"
echo "  3. sudo systemctl start $SERVICE_NAME"

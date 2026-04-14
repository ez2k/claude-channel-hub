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
echo "  ✅ Go $(go version | grep -oP 'go[\d.]+')"
echo "  ✅ Bun $(bun --version)"
echo "  ✅ Claude Code $(claude --version)"
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

# 4. Telegram 플러그인 설치
echo "📦 Telegram 플러그인 확인..."
MARKETPLACE_DIR="$HOME/.claude/plugins/marketplaces/claude-plugins-official/external_plugins/telegram"
CACHE_DIR="$HOME/.claude/plugins/cache/claude-plugins-official/telegram/0.0.5"

if [ -d "$MARKETPLACE_DIR" ]; then
  # 캐시 디렉토리에 복사 + 의존성 설치
  mkdir -p "$CACHE_DIR"
  cp -r "$MARKETPLACE_DIR"/* "$CACHE_DIR/" 2>/dev/null || true
  cp -r "$MARKETPLACE_DIR"/.claude-plugin "$CACHE_DIR/" 2>/dev/null || true
  cp -r "$MARKETPLACE_DIR"/.mcp.json "$CACHE_DIR/" 2>/dev/null || true
  cd "$CACHE_DIR" && bun install --no-summary 2>/dev/null
  echo "  ✅ 공식 플러그인 캐시 준비 완료"
else
  echo "  ⚠️  마켓플레이스에 Telegram 플러그인 없음"
  echo "     Claude Code에서 실행: /plugin install telegram@claude-plugins-official"
fi

# 5. Enhanced server.ts 적용
echo "🔧 Enhanced 플러그인 적용..."
if [ -f "$CACHE_DIR/server.ts" ]; then
  cp "$CACHE_DIR/server.ts" "$CACHE_DIR/server.ts.official.bak" 2>/dev/null || true
  cp "$SCRIPT_DIR/plugins/telegram-enhanced/server.ts" "$CACHE_DIR/server.ts"
  echo "  ✅ 메모리/프로필 기능 적용 완료"
else
  echo "  ⚠️  캐시 디렉토리 없음 — 플러그인을 먼저 설치하세요"
fi

# 6. installed_plugins.json 등록
echo "📝 플러그인 등록 확인..."
PLUGINS_JSON="$HOME/.claude/plugins/installed_plugins.json"
if [ -f "$PLUGINS_JSON" ]; then
  if ! grep -q "telegram@claude-plugins-official" "$PLUGINS_JSON"; then
    python3 -c "
import json
path = '$PLUGINS_JSON'
with open(path) as f:
    data = json.load(f)
data['plugins']['telegram@claude-plugins-official'] = [{
    'scope': 'user',
    'installPath': '$CACHE_DIR',
    'version': '0.0.5',
    'installedAt': '$(date -Is)',
    'lastUpdated': '$(date -Is)'
}]
with open(path, 'w') as f:
    json.dump(data, f, indent=2)
print('  ✅ 플러그인 등록 완료')
"
  else
    echo "  ✅ 이미 등록됨"
  fi
fi

# 7. systemd 서비스 설치
echo ""
echo "🔧 systemd 서비스 설치..."
cp "$SCRIPT_DIR/claude-channel-hub.service" /etc/systemd/system/
systemctl daemon-reload
echo "  ✅ 서비스 등록 완료"
echo ""

# 8. 안내
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
echo "다음 단계:"
echo "  1. .env에 TELEGRAM_BOT_TOKEN 설정"
echo "  2. configs/channels.yaml에 봇/채널 설정"
echo "  3. sudo systemctl start $SERVICE_NAME"

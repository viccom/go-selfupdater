#!/bin/bash
# Setup selfupdate test environment:
#   1. Build testupdater v1.0.0 and v2.0.0
#   2. Start file server hosting v2.0.0 + latest.json
#   3. Start v1.0.0 daemon in background
#   4. Print instructions for CLI and browser testing
set -e

SRCDIR=$(cd "$(dirname "$0")" && pwd)
WORKDIR=/tmp/selfupdate-test
BINDIR="$WORKDIR/bin"
SERVEDIR="$WORKDIR/server"
FILE_PORT=19099
DAEMON_PORT=18080
PID_FILE="$WORKDIR/daemon.pid"

# Cleanup previous runs
if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE")
    kill "$OLD_PID" 2>/dev/null || true
    rm "$PID_FILE"
fi
fuser -k ${FILE_PORT}/tcp 2>/dev/null || true
fuser -k ${DAEMON_PORT}/tcp 2>/dev/null || true
sleep 1

rm -rf "$WORKDIR"
mkdir -p "$BINDIR" "$SERVEDIR/download"

echo "=========================================="
echo "  go-selfupdater 测试环境搭建"
echo "=========================================="
echo ""

# --- Build v1.0.0 ---
echo ">>> 编译 v1.0.0 ..."
cd "$SRCDIR/cmd/testupdater"
sed 's/^const version = ".*"/const version = "1.0.0"/' main.go > "$WORKDIR/main_v1.go"
cp main.go main.go.bak
cp "$WORKDIR/main_v1.go" main.go
go build -o "$BINDIR/testupdater" .
cp main.go.bak main.go
rm main.go.bak "$WORKDIR/main_v1.go"
echo "    $BINDIR/testupdater"

# --- Build v2.0.0 ---
echo ">>> 编译 v2.0.0 ..."
sed 's/^const version = ".*"/const version = "2.0.0"/' main.go > "$WORKDIR/main_v2.go"
cp main.go main.go.bak
cp "$WORKDIR/main_v2.go" main.go
go build -o "$SERVEDIR/download/testupdater" .
cp main.go.bak main.go
rm main.go.bak "$WORKDIR/main_v2.go"
echo "    $SERVEDIR/download/testupdater"

# --- Create latest.json ---
SHA256=$(sha256sum "$SERVEDIR/download/testupdater" | cut -d' ' -f1)
SIZE=$(stat -c%s "$SERVEDIR/download/testupdater")
cat > "$SERVEDIR/latest.json" <<EOF
{
  "version": "2.0.0",
  "date": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "assets": {
    "$(go env GOOS)/$(go env GOARCH)": {
      "url": "http://localhost:${FILE_PORT}/download/testupdater",
      "sha256": "${SHA256}",
      "size": ${SIZE}
    }
  }
}
EOF
echo "    $SERVEDIR/latest.json (v2.0.0, sha256: ${SHA256:0:16}...)"

# --- Add testupdater to PATH ---
export PATH="$BINDIR:$PATH"

# --- Start file server ---
echo ""
echo ">>> 启动文件服务器 (v2.0.0) ..."
cd "$SERVEDIR"
python3 -m http.server $FILE_PORT &> "$WORKDIR/file-server.log" &
echo $! > "$WORKDIR/file-server.pid"
sleep 1
echo "    http://localhost:$FILE_PORT/latest.json"

# --- Start daemon ---
echo ""
echo ">>> 启动 v1.0.0 后台服务 ..."
cd "$WORKDIR"
"$BINDIR/testupdater" daemon "http://localhost:$FILE_PORT/latest.json" "localhost:$DAEMON_PORT" &> "$WORKDIR/daemon.log" &
echo $! > "$PID_FILE"
sleep 1

# Verify daemon
if curl -s "http://localhost:$DAEMON_PORT/api/version" > /dev/null 2>&1; then
    echo "    daemon 运行中 (PID: $(cat $PID_FILE))"
else
    echo "    !!! daemon 启动失败，查看 $WORKDIR/daemon.log"
    cat "$WORKDIR/daemon.log"
    exit 1
fi

# --- Print instructions ---
echo ""
echo "=========================================="
echo "  测试环境就绪！以下是测试方法："
echo "=========================================="
echo ""
echo "  testupdater 二进制: $BINDIR/testupdater"
echo "  已加入 PATH"
echo ""
echo "--- CLI 测试 ---"
echo ""
echo "  testupdater status          # 查看后台服务状态及版本"
echo "  testupdater check           # 检测新版本"
echo "  testupdater update          # 升级并自动重启"
echo "  testupdater status          # 再次查看版本（应变为 2.0.0）"
echo ""
echo "--- 浏览器测试 ---"
echo ""
echo "  打开 http://localhost:$DAEMON_PORT/"
echo "  点击「版本检测」→ 检测到 v2.0.0"
echo "  点击「升级到 2.0.0」→ 自动升级并重启"
echo ""
echo "--- 手动停止 ---"
echo ""
echo "  testupdater stop"
echo "  # 或 kill \$(cat $PID_FILE)"
echo ""
echo "--- 日志 ---"
echo ""
echo "  daemon:  tail -f $WORKDIR/daemon.log"
echo "  server:  tail -f $WORKDIR/file-server.log"
echo ""

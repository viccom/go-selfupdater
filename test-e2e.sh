#!/bin/bash
# End-to-end selfupdate test: build v1 + v2, serve v2, update v1 → v2
set -e

WORKDIR=$(mktemp -d)
SRCDIR=$(cd "$(dirname "$0")" && pwd)
BINDIR="$WORKDIR/bin"
SERVEDIR="$WORKDIR/serve"

# Kill stale processes from previous runs
fuser -k 19099/tcp 2>/dev/null || true
fuser -k 18099/tcp 2>/dev/null || true
sleep 1

echo "=== go-selfupdater e2e test ==="
echo "workdir: $WORKDIR"

mkdir -p "$BINDIR" "$SERVEDIR/download"

# --- Build v1.0.0 ---
echo ""
echo ">>> Building v1.0.0 ..."
cd "$SRCDIR/cmd/testupdater"
sed 's/^const version = ".*"/const version = "1.0.0"/' main.go > "$WORKDIR/main_v1.go"
cp main.go main.go.bak
cp "$WORKDIR/main_v1.go" main.go
go build -o "$BINDIR/testupdater-v1" .
cp main.go.bak main.go
rm main.go.bak "$WORKDIR/main_v1.go"
echo "    v1.0.0 → $BINDIR/testupdater-v1"

# --- Build v2.0.0 ---
echo ">>> Building v2.0.0 ..."
sed 's/^const version = ".*"/const version = "2.0.0"/' main.go > "$WORKDIR/main_v2.go"
cp main.go main.go.bak
cp "$WORKDIR/main_v2.go" main.go
go build -o "$BINDIR/testupdater-v2" .
cp main.go.bak main.go
rm main.go.bak "$WORKDIR/main_v2.go"
echo "    v2.0.0 → $BINDIR/testupdater-v2"

# --- Compute SHA256 of v2 ---
SHA256=$(sha256sum "$BINDIR/testupdater-v2" | cut -d' ' -f1)
SIZE=$(stat -c%s "$BINDIR/testupdater-v2")
echo "    sha256: $SHA256"
echo "    size:   $SIZE"

# Copy v2 binary to serve directory
cp "$BINDIR/testupdater-v2" "$SERVEDIR/download/testupdater"

# --- Create release JSON ---
cat > "$SERVEDIR/latest.json" <<EOF
{
  "version": "2.0.0",
  "date": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "assets": {
    "linux/amd64": {
      "url": "http://localhost:19099/download/testupdater",
      "sha256": "$SHA256",
      "size": $SIZE
    }
  }
}
EOF

# --- Start static file server for v2 ---
echo ""
echo ">>> Starting update server on :19099 ..."
cd "$SERVEDIR"
python3 -m http.server 19099 &
HTTP_PID=$!
sleep 1

echo "    release: http://localhost:19099/latest.json"
echo "    binary:  http://localhost:19099/download/testupdater"

# --- Run v1 and test ---
echo ""
echo ">>> Running v1.0.0 ..."
"$BINDIR/testupdater-v1" version

echo ""
echo ">>> CLI check ..."
"$BINDIR/testupdater-v1" check http://localhost:19099/latest.json

echo ""
echo ">>> CLI update ..."
"$BINDIR/testupdater-v1" update http://localhost:19099/latest.json

echo ""
echo ">>> Version after update (same binary, replaced in-place) ..."
"$BINDIR/testupdater-v1" version

echo ""
echo "=== REST API test ==="
echo ">>> Starting v1 agent ..."
"$BINDIR/testupdater-v1" agent http://localhost:19099/latest.json :18099 &
AGENT_PID=$!
sleep 1

echo ">>> GET /api/version ..."
curl -s http://localhost:18099/api/version | python3 -m json.tool

echo ""
echo ">>> GET /api/check ..."
curl -s http://localhost:18099/api/check | python3 -m json.tool

echo ""
echo ">>> POST /api/update ..."
curl -s -X POST http://localhost:18099/api/update 2>&1

sleep 3

echo ""
echo ">>> GET /api/version (after restart) ..."
curl -s http://localhost:18099/api/version 2>&1 || echo "(process restarted - check above output)"

# Cleanup
kill $AGENT_PID $HTTP_PID 2>/dev/null
wait $AGENT_PID $HTTP_PID 2>/dev/null

echo ""
echo "=== Done ==="
echo "test files: $WORKDIR"
echo "run 'rm -rf $WORKDIR' to clean up"

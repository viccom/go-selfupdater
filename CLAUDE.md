# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go library for self-updating Go binaries. Provides version checking, binary download with SHA256 validation, atomic replacement with rollback, and process restart. Also ships an optional REST API server (`Server`) for daemon-style programs.

Module: `github.com/viccom/go-selfupdater`

## Build & Test

```bash
go test ./...                              # all tests
go test -run TestCompareVersions ./...     # single test
go test -v ./...                           # verbose
go build ./...                             # build library (no output binary)

# Build test harness binary
go build -o testupdater ./cmd/testupdater
```

### E2E Test

```bash
./setup-test.sh    # builds v1+v2, starts mock server + daemon, prints test instructions
./test-e2e.sh      # automated full v1→v2 upgrade flow (non-interactive)
```

Both scripts compile `cmd/testupdater` with different version constants via sed, serve v2 via python http.server, and verify the upgrade path. They require Linux (uses `fuser`, `stat -c`).

## Architecture

```
selfupdate.go   — Updater struct, Check/Update/UpdateAndRestart/DoRestart, ProgressState
source.go       — Source interface, HTTPSource (fetches latest.json), Release/Asset types
replace.go      — DownloadAndReplace, download with retry, SHA256 validation, atomic binary replacement
version.go      — SemVer 2.0 comparison (CompareVersions, IsNewer, ValidateVersion)
server.go       — REST API server (/api/version, /api/check, /api/update, /api/progress)
restart_unix.go — !windows: syscall.Exec (in-process replace)
restart_windows.go — windows: exec.Command + CREATE_NEW_PROCESS_GROUP
util.go         — DefaultLogger, executablePath helper
```

### Key Design Decisions

- **Atomic replacement**: `rename(old→.old) → copyFile(new→old) → chmod`. Rollback on copy/chmod failure. `.old` backup persists for manual recovery.
- **Platform split**: `restart_unix.go` / `restart_windows.go` via build tags. Linux uses `syscall.Exec` (PID-preserving); Windows spawns new process group with 2s health check then `os.Exit`.
- **Exe path caching**: `Update()` caches the resolved exe path before replacement. On Linux, `/proc/self/exe` may point to deleted `.old` after replacement, so `DoRestart()` uses the cached path.
- **Temp file placement**: Downloads to same directory as the executable to avoid cross-filesystem rename failures.
- **Concurrency**: `TryLock` on update mutex — returns error immediately if update is already in progress rather than blocking.
- **Security**: `HTTPSource` auto-upgrades `http://` to `https://` for non-localhost URLs. Optional `WithAuthToken` for mutating endpoints using constant-time comparison. JSON response body limited to 1MB.

### REST API Endpoints (Server)

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/version` | GET | no | Current version, platform, exe path |
| `/api/check` | GET | no | Check for newer release |
| `/api/update` | POST | yes* | Async update + restart (returns immediately, poll `/api/progress`) |
| `/api/progress` | GET | no | Download progress snapshot |

*Auth required only if `WithAuthToken` is configured. Read-only endpoints are always unauthenticated.

### Release Manifest Format

The `latest.json` endpoint that `HTTPSource` fetches:

```json
{
  "version": "1.2.3",
  "date": "2025-01-01T00:00:00Z",
  "assets": {
    "linux/amd64":   {"url": "...", "sha256": "...", "size": 12345},
    "windows/amd64": {"url": "...", "sha256": "...", "size": 12345}
  }
}
```

Asset keys are `os/arch` strings from `runtime.GOOS + "/" + runtime.GOARCH`.

### Progress Tracking

`ProgressState` is thread-safe (RWMutex). Call `updater.Progress().Snapshot()` for a typed copy. Phases: `downloading` → `validating` → `replacing` → `done`. Progress callback reports every 1MB or 500ms during download.

### Windows Considerations

- `.old` backup is locked by the running process; cleanup must happen at next startup via `CleanupStaleBackup()`
- New process gets `CREATE_NEW_PROCESS_GROUP` to detach from parent console
- 2-second health check before parent exits: detects immediate crashes (missing DLL, config errors)

## Test Harness (cmd/testupdater)

Multi-mode CLI for testing the library. Modes: `daemon` (HTTP server with web UI), `status`/`check`/`update`/`stop` (CLI client), `serve` (mock update file server that serves its own binary as v2). The embedded `index.html` provides a browser UI with progress polling.

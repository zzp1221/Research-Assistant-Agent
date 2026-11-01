# ResearchAssistant Desktop

Windows desktop shell for ResearchAssistant. The shell is a thin Wails window:
it starts the Go backend and sandbox as child processes, serves the React
frontend over a fixed loopback origin and owns the process lifecycle.

## Packages

| Package | Responsibility |
|---|---|
| `paths` | Per-user install/data/log directories |
| `settings` | `settings.json`, environment injection, DPAPI secret store |
| `supervisor` | Single-instance lock, port allocation, health checks, Job Object cleanup |
| `proxy` | Loopback reverse proxy with SSE pass-through and SPA fallback |
| `shell` | Orchestrates settings → ports → tokens → supervisor → proxy; exposes `/desktop/state`, `/desktop/diagnostics`, `/desktop/retry` and the error page |
| `diagnostics` | Redacted support bundle (logs tail, settings, health, system, failed plans) |
| `updater` | `latest.json` check, semver compare, SHA-256 + Ed25519 verification, staged download |
| `onboarding` | First-run state, credential validation and minimal completion test |
| `bridge` | Wails-bound API consumed by `window.go.main.API` |
| `tray` | Tray/notification contract (memory implementation for headless builds) |

## Build

```bash
# Linux/CI: pure logic packages and tests
go test ./...

# Windows cross-build without Wails (stub entrypoint)
GOOS=windows GOARCH=amd64 go build ./...

# Real Wails entrypoint
GOOS=windows GOARCH=amd64 go build -tags "desktop wails" ./...
```

The frontend build output must be copied to `webui/dist` before a release
build; the checked-in placeholder only keeps the embed directive valid.

## Runtime layout

```
%LOCALAPPDATA%\Programs\ResearchAssistant\   ResearchAssistant.exe backend.exe sandbox.exe uv.exe webview\
%LOCALAPPDATA%\ResearchAssistant\            settings.json secrets.dat lock data\ runtimes\ workspaces\
```

## Notes

- `main.go` (`desktop && !wails`) prints the resolved paths and keeps
  `go build ./...` green on machines without Wails.
- The shell never serves API tokens to the WebView; the proxy injects
  `Authorization` and rejects non-loopback Host headers.
- AppContainer/lowil execution lives in
  `research-assistant/docker-sandbox/internal/engine/native`; this module only
  selects the runner through settings.

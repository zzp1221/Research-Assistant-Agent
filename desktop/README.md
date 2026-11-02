# ResearchAssistant Desktop

Windows desktop shell for ResearchAssistant. The shell is a thin MyGo window:
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
| `bridge` | Desktop API bound with `mygo.Bind`; the Wails fallback still exposes `window.go.main.API` |
| `mygo` | MyGo adapter: tray, dialogs, notifications and shell integration |
| `tray` | Tray/notification contract (MyGo driver, systray fallback, memory driver for tests) |

## Build

```bash
# Linux/CI: pure logic packages and tests
go test ./...

# Windows cross-build of the default MyGo entrypoint
GOOS=windows GOARCH=amd64 go build ./...

# Wails fallback entrypoint
GOOS=windows GOARCH=amd64 go build -tags "desktop wails" ./...

# Headless stub: prints resolved paths, opens no window
GOOS=windows GOARCH=amd64 go build -tags headless ./...
```

Release builds use `desktop/build/build.ps1`; it builds the frontend, the
backend and sandbox executables, runs `mygo build` and packages the result
with the existing NSIS script. `desktop/mygo.json` pins the MyGo app name,
version, resources and bindings. The NSIS wizard lets the user choose the
install directory and optionally the PDF association.

The frontend build output must be copied to `webui/dist` before a release
build; `build.ps1` does it. The checked-in placeholder only keeps the embed
directive valid.

Run the produced installer from a normal user folder such as Desktop or
Downloads. NSIS needs to create a temporary file next to the installer, and a
sandboxed workspace ACL that denies `DeleteSubdirectoriesAndFiles` makes it
fail with `Error writing temporary file. Make sure your temp folder is valid.`
See [desktop/build/README.md](build/README.md).

`mygo generate` writes the typed client to
`research-assistant/frontend/src/services/mygo.ts`. Run it after changing the
bound `bridge.API` methods or the declared events.

## Runtime layout

```
%LOCALAPPDATA%\Programs\ResearchAssistant\   ResearchAssistant.exe backend.exe sandbox.exe uv.exe
%LOCALAPPDATA%\ResearchAssistant\            settings.json secrets.dat lock data\ runtimes\ workspaces\
```

## Notes

- `main_mygo.go` is the default entrypoint (build tag `!wails`); it opens the
  MyGo window, binds `bridge.API` and loads the shell's loopback proxy so the
  existing same-origin frontend, SSE, uploads and downloads are unchanged.
- `main_wails.go` remains as the Wails fallback behind `-tags "desktop wails"`.
- `main.go` is the headless stub behind `-tags headless`.
- MyGo is pinned to v0.7.4. Its API is used only through `desktop/mygo`, so a
  MyGo upgrade stays in one place.
- The shell never serves API tokens to the WebView; the proxy injects
  `Authorization` and rejects non-loopback Host headers.
- AppContainer/lowil execution lives in
  `research-assistant/docker-sandbox/internal/engine/native`; this module only
  selects the runner through settings.

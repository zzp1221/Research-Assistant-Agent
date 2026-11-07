# scripts

[简体中文](README.md) | English

Local startup and Docker Compose helper scripts. All scripts use `research-assistant/` as the working root and read `backend.env`.

## Architecture diagrams

- [System architecture](../../docs/diagrams/architecture.drawio) ([SVG preview](../../docs/diagrams/architecture.svg))
- [Runtime flow](../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../docs/diagrams/runtime-flow.svg))

Scripts cover local development only; containerized deployment should use `docker compose up --build -d`. Windows desktop users should use the installer or `build.ps1` from [desktop/README.md](../../desktop/README.md).

## Repository-root scripts

| Script | Description |
|---|---|
| `container-go.sh` | Runs `go build/vet/test` in a fixed `golang:1.27.3-bookworm` container (mounts the repo and Go cache; workdir is `backend/`) |
| `ra.mjs` | Zero-dependency API CLI: create/list/status/events/execute/pause/resume/cancel/approve/wait/export/upload/search/import/openapi/health |
| `check-utf8.mjs` | Checks UTF-8 validity and mojibake in repository text files |
| `mock-audit.mjs` | Scans for forbidden mock/hard-coded markers in the backend to prevent fake implementations |

`ra.mjs` defaults to `http://localhost:18080`; override with `RA_API_URL` / `RA_API_TOKEN` / `RA_COOKIE_FILE`.

## Unix

| Script | Description |
|---|---|
| `unix/start-backend.sh` | Start the Go API (`:8080`) |
| `unix/start-sandbox.sh` | Start the Go sandbox service (`:8082`, engine mode) |
| `unix/start-frontend.sh` | Start the Vite dev server (`:5173`) |
| `unix/docker-up.sh` | `docker compose up --build -d` |
| `unix/docker-down.sh` | `docker compose down` |

## Windows

| Script | Description |
|---|---|
| `windows/start-backend.ps1` / `.cmd` | Legacy local backend starter; it still prints a Linux-only notice. Prefer the desktop client or Docker Compose on Windows |
| `windows/start-sandbox.ps1` / `.cmd` | Start the Go sandbox service |
| `windows/start-frontend.ps1` / `.cmd` | Start Vite |
| `windows/docker-up.ps1` / `.cmd` | Start Docker Compose |
| `windows/docker-down.ps1` / `.cmd` | Stop Docker Compose |

PowerShell scripts clear proxy variables, create `.gocache`, and read `backend.env`. If `OPENAI_API_KEY` is missing, the startup script reports a clear error.

> The desktop client starts `backend.exe` and `sandbox.exe` itself and does not need these scripts. Linux containers remain the recommended server deployment; the backend builds on Windows through build-tag abstractions, but some POSIX features are limited there.

## Usage

```bash
./scripts/unix/start-sandbox.sh
./scripts/unix/start-backend.sh
./scripts/unix/start-frontend.sh
```

Windows:

```powershell
.\scripts\windows\start-sandbox.ps1
.\scripts\windows\start-frontend.ps1
# Backend: prefer the desktop client, or docker compose up --build -d
```

## Notes

Scripts are for local development only; they do not provide production process supervision, log rotation, or multi-replica orchestration. Production should use container orchestration and dedicated secret management.

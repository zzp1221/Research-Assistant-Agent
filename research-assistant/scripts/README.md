# scripts

本地启动与 Docker Compose 辅助脚本。所有脚本都以 `research-assistant/` 为工作根目录，并读取 `backend.env`。

## 相关架构图

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）

脚本只负责本地开发编排；容器化部署优先使用 `docker compose up --build -d`。

## 仓库根脚本

| 脚本 | 说明 |
|---|---|
| `container-go.sh` | 在固定 `golang:1.26.1-bookworm` 容器内执行 `go build/vet/test`（挂载仓库与 Go 缓存卷） |
| `ra.mjs` | 零依赖 API CLI：create/status/events/execute/pause/resume/cancel/approve/wait/export/upload/search/import/openapi |
| `check-utf8.mjs` | 检查仓库文本文件 UTF-8 合法性 |
| `mock-audit.mjs` | 扫描后端禁止的 mock/硬编码标记，防止假实现回归 |

## Unix

| 脚本 | 说明 |
|---|---|
| `unix/start-backend.sh` | 启动 Go API（`:8080`） |
| `unix/start-sandbox.sh` | 启动 Go 沙箱服务（`:8082`） |
| `unix/start-frontend.sh` | 启动 Vite 开发服务器（`:5173`） |
| `unix/docker-up.sh` | `docker compose up --build -d` |
| `unix/docker-down.sh` | `docker compose down` |

## Windows

| 脚本 | 说明 |
|---|---|
| `windows/start-backend.ps1` / `.cmd` | 明确提示使用 Docker Compose（Windows 原生后端不受支持） |
| `windows/start-sandbox.ps1` / `.cmd` | 启动 Go 沙箱服务 |
| `windows/start-frontend.ps1` / `.cmd` | 启动 Vite |
| `windows/docker-up.ps1` / `.cmd` | Docker Compose 启动 |
| `windows/docker-down.ps1` / `.cmd` | Docker Compose 停止 |

PowerShell 脚本会清理代理环境变量、创建 `.gocache` 并读取 `backend.env`。如果 `OPENAI_API_KEY` 未配置，启动脚本会给出明确错误。

> Windows 原生后端不受支持：后端依赖 Linux 进程组、`syscall.Kill` 与 `Statfs`。Windows 上请使用 `docker compose up --build -d`；`start-backend.ps1` 现在会直接提示该方式。

## 使用

```bash
./scripts/unix/start-sandbox.sh
./scripts/unix/start-backend.sh
./scripts/unix/start-frontend.sh
```

Windows：

```powershell
.\scripts\windows\start-sandbox.ps1
.\scripts\windows\start-backend.ps1
.\scripts\windows\start-frontend.ps1
```

## 注意

脚本只用于本地开发，不负责生产进程守护、日志轮转或多副本编排。生产部署请使用容器编排和独立密钥管理。

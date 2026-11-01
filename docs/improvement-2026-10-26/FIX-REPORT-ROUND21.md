# 修复报告：运行时安装触发 + Docker 升级提示 + 阶段状态修正（第二十一轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机/uv 实际下载未验证。

## 1. 本轮完成

### 1.1 运行时安装触发（5.4 / 6.3.1）

- native Provider 新增 InstallRuntime：调用 RuntimeManager.EnsureRuntime 并返回 RuntimeStatus。
- sandbox 新增 POST /api/v1/runtime/install（native 返回 supported=true；其他 provider supported=false）。
- shell 新增 InstallRuntime + POST /desktop/runtime/install（超时 10 分钟）。
- bridge 新增 InstallRuntime；RuntimePanel 新增“准备 Python 运行时”按钮与 preparing/ready/error 状态。
- 伪 uv 脚本单测验证 EnsureRuntime/InstallRuntime（venv 创建、pip freeze、状态统计）。

### 1.2 Docker 升级提示（P4.1）

- RuntimePanel 检测到 capabilities.provider=docker 且当前偏好不是 docker 时，显示“检测到 Docker，建议使用容器模式”提示与“切换到 Docker”按钮。
- 一键切换复用 UpdateProvider 持久化流程。

### 1.3 阶段状态修正

- 设计文档 15 节 P2/P3/P4 标题由“未开始”改为“部分完成”，与顶部状态表一致。

## 2. 实际与文档不一致处

- 文档 5.4 的“下载 Python 运行时”此前只有说明，没有触发接口；本轮补 InstallRuntime 链路。
- 文档 6.3.1 的运行时管理此前只有状态展示；本轮补安装触发。
- 文档 P4.1 的 Docker 升级提示此前没有 UI；本轮补齐。
- 文档 15 节标题与状态表不一致；本轮修正。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | go build/test + Windows 交叉编译 | 通过（含 InstallRuntime fake uv） |
| desktop 全量 | go test ./... | 通过（bridge InstallRuntime、shell InstallRuntime） |
| desktop Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... + Wails tag | 通过 |
| backend 全量 | go build/vet/test | 通过 |
| 前端 | npm test、npm run build、npm run lint | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- 真实 uv 下载进度百分比/断点续传未实现；UI 只显示“准备中”与结果。
- Windows 真机 WSL2/AppContainer/签名/安装未验证。
- 安装运行时是同步请求，超长下载可能受 HTTP 超时影响（当前 10 分钟）。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。

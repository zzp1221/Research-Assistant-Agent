# 修复报告：运行时安装进度 + UI 轮询 + 回归（第二十二轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机/uv 实际下载未验证。

## 1. 本轮完成

### 1.1 安装阶段进度（5.4 / 6.3.1）

- RuntimeManager 新增 EnsureRuntimeWithProgress，阶段：python → venv → dependencies（仅有 requirements 时）→ freeze → done；缓存命中为 reused。
- 原 EnsureRuntime 保持兼容（内部调用 progress=nil）。
- native Provider 记录 installing/stage/install_error，RuntimeStatus 返回。
- 伪 uv 单测验证阶段集合与顺序，InstallRuntime 成功状态。

### 1.2 UI 轮询

- RuntimePanel 安装期间每 2s 轮询 /desktop/runtime，显示阶段（starting/python/venv/...）与错误。
- shell RuntimeStatusView 透传 installing/stage/install_error。

## 2. 实际与文档不一致处

- 文档 5.4 的“下载进度”此前只有同步按钮；本轮补阶段级进度（非百分比）。
- 文档 6.3.1 的运行时状态此前只有结果；本轮补安装中状态。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | go build/test + Windows 交叉编译 | 通过（含进度阶段测试） |
| desktop 全量 | go test ./... | 通过（shell RuntimeStatus 透传） |
| desktop Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... + Wails tag | 通过 |
| backend 全量 | go build/vet/test | 通过 |
| 前端 | npm test、npm run build、npm run lint | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- 阶段级进度，不提供下载百分比/字节数/断点续传。
- Windows 真机 uv 下载、WSL2/AppContainer/签名/安装未验证。
- 安装仍是同步 HTTP 请求，10 分钟超时。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。

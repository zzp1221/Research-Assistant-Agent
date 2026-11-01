# 修复报告：运行时缓存清理 + 回归（第二十五轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机未验证。

## 1. 本轮完成

### 1.1 运行时缓存清理（6.3.1 / 10）

- native 新增 CleanRuntimeCache：删除 RuntimeRoot/VenvRoot 后重建空目录。
- Provider.CleanupRuntimeCache：安装中拒绝（installMu），清理后返回 RuntimeStatus（venv=0/disk≈0）。
- sandbox 新增 POST /api/v1/runtime/cleanup（native 返回 supported=true；其他 supported=false）。
- shell 新增 CleanupRuntimes + POST /desktop/runtime/cleanup。
- bridge 新增 cleanupRuntimes；RuntimePanel 新增“清理运行时缓存”按钮（安装/清理中禁用，二次确认）。
- 单测：临时目录删除/重建、安装中拒绝、shell httptest。

## 2. 实际与文档不一致处

- 文档 6.3.1 的“磁盘占用清理”此前只有展示，没有入口；本轮补齐。
- RuntimePanel 之前只有“准备运行时”，没有清理按钮。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | go test + Windows 交叉编译 | 通过（含 cleanup 单测） |
| desktop 全量 | go test + Windows 交叉编译（Wails tag） | 通过（shell cleanup） |
| backend 全量 | go build/vet/test | 通过（本轮未改） |
| 前端 | npm test/build/lint | 36 文件 / 144 tests 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- 清理是用户手动触发；没有自动配额/GC 策略。
- Windows 真机 WSL2/AppContainer/签名/安装未验证。
- 清理会删除全部运行时/venv，下一次执行需要重新准备。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。

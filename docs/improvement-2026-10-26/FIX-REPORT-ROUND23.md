# 修复报告：更新可用提示 + 系统包能力门禁 + 回归（第二十三轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机/更新服务器未验证。

## 1. 本轮完成

### 1.1 更新可用提示（11/10）

- 新增 useUpdateAvailable hook：监听 Wails runtime 事件 ra:update-available。
- 新增 UpdateAvailableBanner：显示版本、Release Notes 链接，可关闭；浏览器模式无 runtime 时静默。
- 集成到工作台顶部；组件测试覆盖 prop、关闭回调、Wails 事件触发。

### 1.2 系统包能力门禁（6.2）

- runtimeProbe：native provider 且 reproduction_runtime_spec.SystemPackages 非空时，生成 system_package_unavailable，阻断执行。
- attachSandboxCapabilities：计划工件存在同样需求时前置生成缺失项并 blocked。
- 单测覆盖 GPU/Python/SystemPackages 组合门禁。

## 2. 实际与文档不一致处

- 文档 11 的自动更新此前 main_wails 会 emit 事件但前端无监听；本轮补 banner。
- 文档 6.2 的 SystemPackages 需求此前没有门禁；本轮补齐。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| backend 全量 | go build/vet/test | 通过（含门禁单测） |
| docker-sandbox 全量 | go build/test + Windows 交叉编译 | 通过 |
| desktop 全量 | go test ./... + Windows 交叉编译 | 通过 |
| 前端 | npm test、npm run build、npm run lint | 36 文件 / 144 tests 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- 更新 banner 只提示，实际检查/下载/安装仍在设置页。
- Windows 真机更新服务器/签名、WSL2/AppContainer/安装未验证。
- SystemPackages 门禁只在能解析 runtime spec 时触发；普通新计划仍由执行期 runtime_probe 兜底。

## 5. 清理

- 删除 D:\tmp\ra-backend-patch7；临时容器均为 --rm。

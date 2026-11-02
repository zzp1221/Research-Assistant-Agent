# 方案：用 MyGo 替换 Wails 桌面外壳

- 日期：2026-11-02
- 分支：`feature/desk`
- 状态：已实施（2026-11-02；MyGo 0.7.4 默认入口，Wails 回退保留）
- 关联：[Windows 桌面客户端实施方案](2026-10-30-windows-desktop-client.md)、[desktop/README.md](../../desktop/README.md)
- 结论：**可行，推荐只替换 Wails 外壳，不重写现有 desktop/shell、supervisor、proxy、settings、bridge、沙箱 Provider。**

---

## 1. 为什么可以只替换外壳

当前 `desktop/` 已经把“外壳”和“桌面能力”分开了：

| 层 | 当前实现 | 是否依赖 Wails |
|---|---|---|
| 窗口/事件循环/WebView | `desktop/main_wails.go` 的 `wails.Run` + `assetserver` | **是，唯一强依赖** |
| 进程监督 | `desktop/supervisor` | 否 |
| 反向代理 / SSE / SPA 回退 | `desktop/proxy`、`desktop/shell` | 否 |
| 设置 / DPAPI / 环境变量注入 | `desktop/settings` | 否 |
| 前端状态与桌面 API | `desktop/bridge` + `frontend/src/services/desktopBridge.ts` | **桥接方式依赖 Wails 的 `window.go.main.API`** |
| 托盘 / 通知 | `desktop/tray` | Windows 用 `systray`，与 Wails 无关 |
| 更新 / 诊断 / 崩溃 | `desktop/updater`、`desktop/diagnostics`、`desktop/crashdump` | 否 |

因此替换范围可以严格限定为：

1. `desktop/main_wails.go` → 新的 `desktop/main_mygo.go`（MyGo 入口）；
2. `desktop/bridge` 的暴露方式 → 从 Wails 的 `window.go.main.API` 改为 MyGo 的 `mygo.Bind` + 生成 TypeScript client；
3. `desktop/go.mod` 的 Wails 依赖 → MyGo 依赖；
4. 打包脚本从 Wails 产物约定改为 MyGo 产物约定。

**不动**：`desktop/shell`、`desktop/supervisor`、`desktop/proxy`、`desktop/settings`、`desktop/updater`、`desktop/diagnostics`、`desktop/crashdump`、`desktop/tray`、`docker-sandbox/internal/engine/*`。

---

## 2. 目标与验收

| 编号 | 目标 | 验收方式 |
|---|---|---|
| M1 | MyGo 能打开窗口并加载现有 React 构建产物 | Windows 11 上启动，页面与当前 Wails 版本一致 |
| M2 | 前端现有 `desktopBridge` 调用全部可用 | Onboarding、RuntimePanel、诊断、更新、通知、选目录/定位文件逐项通过 |
| M3 | `/api` 与 `/desktop/*` 仍由现有 Shell 提供服务 | `http-smoke`、`desktop-smoke` 与浏览器 DevTools 检查 |
| M4 | backend/sandbox 子进程生命周期不变 | 启动、健康检查、重启、退出无残留进程 |
| M5 | Windows 安装包/签名/自动更新可用 | `mygo build` 产出安装器；签名与更新校验通过 |
| M6 | 不破坏容器回归 | 现有 Go/前端/E2E 全绿 |

---

## 3. 关键设计

### 3.1 入口：MyGo 只做窗口，不做业务

新增 `desktop/main_mygo.go`（build tag `desktop && mygo`），职责只有：

1. 解析 `paths`、加载 `settings`、创建 `shell.New(...)`；
2. 用 `mygo.SetFrontend(webAssets)` 或 `shell.Handler()` 提供前端；
3. `mygo.App.WhenReady` 中创建窗口并 `shell.Start(ctx)`；
4. `mygo.App.OnBeforeQuit` / `OnWillQuit` 中调用 `shell.Stop`；
5. 把 `bridge.API` 通过 `mygo.Bind(api)` 暴露给页面。

不把 `shell` 改造成 MyGo 组件，避免业务逻辑被 UI 框架绑死。

### 3.2 前端加载：两种可选路径

**方案 A（推荐）：MyGo 静态前端 + 自定义协议转发 API**

- `mygo.SetFrontend(fs.Sub(webAssets, "webui/dist"))` 提供页面；
- `mygo.Protocol.HandleFunc("api", ...)` 把 `api://` 请求转给 `shell.Handler()`；
- 页面内 `window.location.origin` 变为 `mygo://localhost`（Windows 为 `http://mygo.localhost`），前端现有同源逻辑基本不用改；
- 需要把前端 `API_BASE_URL` 从 `window.location.origin` 调整为 MyGo 的 API scheme，或在 `mygo.SetFrontend` 前注入 `window.__RA_API_BASE__`。

**方案 B（最稳）：MyGo 窗口直接加载现有 loopback 代理**

- `shell.Start` 已经监听 `127.0.0.1:<port>` 并提供 `shell.Handler()`；
- `mygo.NewWindow(mygo.WindowOptions{URL: shell.ProxyURL()})` 加载 `http://127.0.0.1:<port>/`；
- 现有 `proxy`、`/desktop/*`、SSE、Cookie、Host/Origin 守卫全部不变；
- 缺点：窗口 origin 是 loopback，和 Wails 当前 `assetserver` 行为不同，但前端本来就按同源设计，**风险最低**。

**建议先用方案 B 做 PoC**，验证通过后再评估是否迁移到方案 A。方案 B 几乎零前端改动。

### 3.3 Bridge：从 Wails 绑定到 MyGo 绑定

当前：

```
window.go.main.API.NeedsOnboarding()
```

改为 MyGo：

```go
api := bridge.New(...)
mygo.Bind(api)
```

MyGo 会生成 `src/mygo.ts`，前端需要把 `desktopBridge.ts` 的 `wailsBridge()` 分支改为：

```ts
import { API } from './mygo';

function mygoBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined' || !(window as any).mygo) return undefined;
  return {
    needsOnboarding: () => API.needsOnboarding(),
    completeOnboarding: (credentials) => API.completeOnboarding(credentials),
    // ...
  };
}
```

为降低风险，保留 `window.__RA_DESKTOP__` 作为测试注入口，现有组件测试无需改。

### 3.4 托盘、通知、单实例

- 单实例：用 MyGo 的 `mygo.App.RequestSingleInstanceLock()` + `OnSecondInstance`，替换当前 `supervisor.InstanceLock` 的“窗口唤起”职责；`lock` 文件仍可保留作为进程级兜底，但不再负责 UI 激活。
- 托盘：用 MyGo 的 `Tray` API 替换 `getlantern/systray`；`desktop/tray` 接口保持不变，只换 Windows 实现。
- 通知：用 `mygo.NewNotification` 替换 Wails 的 `runtime.EventsEmit("ra:notification")`；前端仍可收到 UI 内通知，同时系统级通知走 MyGo。
- 原生对话框：`ChooseDirectory`、`ShowInFolder`、`OpenLogs` 改用 `mygo.Dialog.Open`、`mygo.Shell.ShowItemInFolder`、`mygo.Shell.OpenPath`。

### 3.5 打包：MyGo 产物 + 我们的安装器

MyGo 的 `mygo build` 输出：

```
build/windows-amd64/
  ResearchAssistant.exe
  <resources>
  ResearchAssistant-setup-<version>-amd64.exe
```

我们的项目需要把：

- `backend.exe`
- `sandbox.exe`
- `uv.exe`
- `webui/dist`

放进 MyGo 的 `resources/`，并在 `mygo.json` / `mygo.config.ts` 中声明 `resources`。

但**不建议直接使用 MyGo 的默认 installer**：我们的 NSIS 脚本已经包含：

- `backend.exe` / `sandbox.exe` / `uv.exe` 的布局；
- `researchassistant://` 协议；
- `.pdf` 关联；
- 卸载时保留用户数据的逻辑。

建议保留 `desktop/build/installer.nsi`，把 MyGo 产出的 `ResearchAssistant.exe` 和相关资源作为输入，继续用现有脚本打包。这样安装行为不变，MyGo 只负责“生成可执行文件 + 签名 + 更新归档”。

### 3.6 版本与依赖

MyGo 当前 `go.mod` 要求 **Go 1.27.3**，我们当前是 **Go 1.26.1**。这是硬前置：

- 先升级 `go.work`、`desktop/go.mod`、`research-assistant/backend/go.mod`、`docker-sandbox/go.mod` 到 `go 1.27.x`；
- Docker 镜像从 `golang:1.26.1-bookworm` 升级到 `golang:1.27-bookworm`（需确认镜像 tag 存在）；
- 跑全量容器测试，确认没有依赖不兼容。

如果暂时不能升级 Go 1.27，MyGo 路线应暂缓，继续 Wails。

---

## 4. 改造清单

| 文件/模块 | 改动 |
|---|---|
| `desktop/go.mod` | 增加 `github.com/egoist/mygo`；移除或保留 Wails 由 build tag 隔离 |
| `desktop/main_mygo.go`（新） | MyGo 入口：窗口、SetFrontend/URL、Bind、生命周期 |
| `desktop/main_wails.go` | 保留作为回退，build tag `desktop && wails` |
| `desktop/main.go` | stub 改为选择 MyGo/Wails/headless 三种构建 |
| `desktop/bridge/bridge.go` | 保持方法签名；新增 MyGo 兼容的导出（首字母大写已满足） |
| `frontend/src/services/desktopBridge.ts` | 增加 MyGo 分支；保留 Wails 分支用于回退 |
| `frontend/vite.config.ts` / 构建脚本 | 生成/输出 `src/mygo.ts`；把 `dist` 交给 MyGo |
| `desktop/build/package.ps1` | 输入从 Wails 产物改为 MyGo 产物；继续调用现有 NSIS |
| `desktop/build/installer.nsi` | 基本不变，确认新产物文件名 |
| `desktop/tray/tray_windows.go` | 用 MyGo Tray 替换 systray（或保留 systray 作为回退） |
| `desktop/README.md` | 更新构建命令、目录、回退说明 |
| CI | 增加 Go 1.27 作业；MyGo 构建作业；保留 Wails 构建作业 |

---

## 5. 分阶段实施

### Phase 0：PoC（1–2 天）

1. 新建 `desktop/poc/mygo-shell`（不接入主构建），用 MyGo web template 验证 Windows 11 窗口、WebView2、托盘、单实例。
2. 把现有 `frontend/dist` 复制进去，验证页面加载。
3. 用 MyGo 窗口加载现有 `shell.Handler()` 的 loopback URL，验证 `/api`、SSE、上传、下载。
4. 验证 `mygo build` 在 Windows 上产出 exe + installer。

**PoC 通过标准**：窗口能打开、页面能操作、`/api/health` 200、SSE 能持续 10 分钟、文件上传下载正常。

### Phase 1：入口与 Bridge（2–3 天）

1. 增加 `desktop/main_mygo.go`，用 build tag `desktop && mygo`。
2. `bridge.API` 用 `mygo.Bind` 暴露；生成 `mygo.ts`。
3. 前端 `desktopBridge.ts` 增加 MyGo 分支。
4. 单测：bridge 方法签名、`getDesktopBridge()` 选择逻辑。

### Phase 2：桌面能力替换（2–3 天）

1. 单实例：MyGo `RequestSingleInstanceLock` + `OnSecondInstance`。
2. 托盘：MyGo Tray。
3. 通知：MyGo Notification。
4. 原生对话框：MyGo Dialog/Shell。
5. 保留 `desktop/tray` 的接口，方便回退。

### Phase 3：打包与签名（2–3 天）

1. MyGo `mygo build` 产出 `ResearchAssistant.exe`。
2. 把产物交给现有 `desktop/build/package.ps1` + `installer.nsi`。
3. 配置 `windows.certificate` 或 `signCommand`。
4. 配置 `updates.publicKey` + `updates.github` 或 `updates.url`，验证 `mygo.Updater`。
5. 验证安装/卸载/升级/回滚。

### Phase 4：回归与切换（2 天）

1. Windows 11 真机：窗口、托盘、单实例、通知、对话框、安装、升级。
2. 容器内：backend/docker-sandbox/desktop 全量测试。
3. 前端：`npm test`、`npm run build`、`npm run lint`。
4. 默认构建切到 MyGo；Wails 保留为 `-tags wails` 回退。

---

## 6. 风险与对策

| 风险 | 概率 | 影响 | 对策 |
|---|---|---|---|
| Go 1.27 升级破坏现有依赖 | 中 | 高 | 先单独升级并跑全量容器测试；不能过则暂缓 |
| MyGo 很年轻（v0.7.4，82 open issues） | 中 | 中 | pin tag、vendor、保留 Wails 回退分支 |
| MyGo 的 WebView2 origin/信任模型与现有前端不一致 | 中 | 高 | 优先用方案 B（加载 loopback URL），避免改前端同源逻辑 |
| SSE / 大文件 Range / 上传兼容性 | 中 | 高 | PoC 专项测试 40 分钟 SSE、32MB 上传、Range 下载 |
| MyGo 默认 installer 不满足我们的数据保留/协议/文件关联 | 高 | 中 | 保留现有 NSIS 脚本，把 MyGo exe 作为输入 |
| MyGo API 在 0.x 版本变化 | 中 | 中 | 锁定 v0.7.4，封装在 `desktop/mygo` 适配层，业务只依赖适配层 |
| 托盘/通知与现有 systray 行为差异 | 低 | 低 | `desktop/tray` 接口不变，替换实现即可 |
| Windows 真机不可用 | 高 | 高 | 必须准备 Windows 11 测试机；Linux 容器只能做编译和纯逻辑单测 |

---

## 7. 验收清单

- [ ] Go 1.27.x 升级后 backend/docker-sandbox/desktop 容器全量测试通过
- [ ] MyGo 窗口加载现有前端，页面无白屏、无 CORS 错误
- [ ] `/api/health`、`/api/sandbox/capabilities`、SSE、上传、下载全部通过
- [ ] 现有 `desktopBridge` 方法全部可用（Onboarding/RuntimePanel/诊断/更新/通知/目录选择）
- [ ] 单实例第二次启动唤起已有窗口
- [ ] 托盘显示/退出正常
- [ ] 关闭窗口到托盘/退出无残留 backend/sandbox 进程
- [ ] `mygo build` 产出 Windows exe + 安装器；签名通过
- [ ] 自动更新：旧版本能检测、下载、校验、安装新版本
- [ ] NSIS 安装/卸载保留用户数据；协议和 PDF 关联正常
- [ ] Wails 回退构建仍可用（`-tags "desktop wails"`）
- [ ] 文档、CI、README 更新

---

## 8. 结论与建议

**建议采用，但按“外壳适配层”方式接入，不做全量重写。**

- 第一阶段只做 PoC，不切主构建；
- PoC 通过后再把 `desktop/main_mygo.go` 作为默认入口；
- 保留 `main_wails.go` 作为回退；
- 现有 `shell/supervisor/proxy/settings/bridge` 不重写，只替换 UI 框架绑定；
- 安装包继续用现有 NSIS 脚本，MyGo 只负责生成 exe 和签名/更新。

这样能把风险控制在“窗口层”，同时获得 MyGo 带来的托盘、单实例、原生对话框、安装包、签名和自动更新能力。

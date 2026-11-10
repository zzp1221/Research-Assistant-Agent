# 工作台交互改进实施与验证报告

- 日期：2026-11-09（Asia/Shanghai）。
- 分支：`feature/workbennch`；代码基线：`e6bfb26`；本报告对应随该分支提交的实现；2026-11-10 整理为 GitHub PR。
- 需求：[工作台交互改进方案](2026-11-09-workbench-interaction-improvements.md)。原方案保留设计稿身份，验收证据在本报告记录。
- 验证状态：实现及本报告所列自动化、真实 API/模型验收完成；原生桌面关闭与物理设备检查边界见末节。

## 实现范围

| 批次 | 用户可见变化 | 实现边界 |
|---|---|---|
| UI-01 | 审阅区标明编辑版本、执行版本和草稿状态；保存后保留面板；修改后需保存并校验才可批准运行；关闭、切版本和切计划保护草稿 | 归一化比较覆盖嵌套 inputs 和非法数值；正数 base_revision 乐观锁；历史版本只读并显式派生；统一流程锁和请求 generation；结果不明先核对服务端；审批成功和执行成功分别呈现 |
| UI-02 | 打开运行中计划自动接续；连接状态与运行状态分别显示；等待审批、回答、暂停、终态各有相应入口 | 统一 observePlan 读取快照、回填历史、按 seq 续接；身份、计划和运行边界隔离旧回调；fresh ticket、有限重连、聚焦核对；服务端持久化编号事件后发布，并保护 revision 与普通执行的并发入口 |
| UI-03 | 宽屏可停靠详情，中屏为抽屉，手机为单主区；聊天可收起且草稿保留；键盘可打开、调整和关闭 | 容器宽度决定模式；聊天/画布/详情最小宽度 320/480/360px；首选宽度与临时有效宽度分开；兼容旧缓存；原生 dialog 约束焦点和背景交互；可视区域高度响应软键盘与偏移 |
| UI-04 | 论文和对话并排，手机切换阅读/对话；划词直接添加来源引用；普通追问与相邻页连续对话；返回原页 | 前后端 document_references 契约、所有权和页码校验、预算内提取原文与相邻页、持久化会话来源；阅读页码/缩放按身份、会话和 upload 保存；下载、辅助解释和翻译任务隔离旧响应；关闭文档释放资源，暂时收起保留任务 |

由主代理统筹审阅、运行和 PDF 三个子代理，四条实现路径均实际调用 `dsh --profile wb-cli`。DSH 产出经独立源码复核和回归修正后合入当前工作区，未修改全局 DSH 配置。

## 验证环境

Windows 本机使用 PowerShell、Go 1.27.3、Node 24.14.0、npm 11.9.0 和已安装 Google Chrome。开发前端使用 `127.0.0.1:15173`；最终浏览器验收使用 `127.0.0.1:15174` 的 Vite 静态生产预览，构建时指定 `VITE_API_BASE_URL=http://127.0.0.1:18080`。真实 API 的数据、文档、模型配置和日志全部隔离在 `%TEMP%/ra-workbench-verification`，未使用用户原有计划库。

真实 PDF 使用 pdf-lib 生成三页原文；提取使用本机 pdftotext/pdfinfo；普通 chat 使用已有环境中的实际模型服务。临时验证环境显式设置 `MODEL_CONTEXT_WINDOW_TOKENS=128000`，不记录密钥。长运行计划使用所有者私有的原生 sleep 工具，持续 48 秒，经过真实审批、revision 执行、调度、存储与 SSE 链路；它验证恢复与版本一致性，不覆盖外部科研沙箱任务质量。

Linux 回归使用 `golang:1.27.3-bookworm`、独立临时源码副本和 Go 缓存卷，安装 Poppler、Tesseract 及中英文识别数据。测试副本同时包含模块外的 `research-assistant/test` 和 `research-assistant/examples`，以满足原有测试的相对路径。

## 命令与结果

| 命令 | 最终结果 | 原始证据 |
|---|---|---|
| `npm run build` | 通过 TypeScript 与生产构建；保留 PDF.js eval、Vite 大 chunk 的依赖警告 | `frontend-build.log` |
| `npm run lint` | 通过，0 error、0 warning | `frontend-lint.log` |
| `npm test` | Node 单元测试 21/21，Vitest 56 文件 486/486 | `frontend-tests.log` |
| `npm run check:bundle` | 通过：入口 JS gzip 234.5 KB、CSS 17.1 KB、PDF lazy 146.6 KB，分别小于 320/35/400 KB 预算 | `frontend-bundle.log` |
| 全部 Playwright，生产预览＋真实 API | 37 个用例最终均通过：整套 36 通过；真实 PDF 的旧选区坐标等待修正后单独 1/1 通过，20.4s；生产代码没有为此改动 | `final-browser.log`、`final-browser/`、`live-pdf-final.log`、`live-pdf-final/` |
| Windows `go test ./...` | 未全部通过：13 个现有平台相关失败；api、memory 及本次变更相关包通过 | `backend-tests-windows.log` |
| Windows `go vet ./...` | 通过 | `backend-vet.log` |
| Windows `go test -race ./internal/api` | 通过，8.916s | `backend-api-race.log` |
| Linux `go test ./... && go vet ./...` | 全量通过，包含中英文 PDF/OCR、revision/stream、API、memory、scheduler 和模块外集成 fixtures | `backend-tests-linux.log` |

原始日志、浏览器 trace、网络计数和截图位于 `reports/workbench-2026-11-09/`；按仓库规则这些生成证据不随 Git 提交。最终浏览器命令如下，在 `research-assistant/frontend` 的 PowerShell 执行：

```powershell
$env:PLAYWRIGHT_CHROME_PATH='C:\Program Files\Google\Chrome\Application\chrome.exe'
$env:E2E_BASE_URL='http://127.0.0.1:15174'
$env:WORKBENCH_LIVE_API='http://127.0.0.1:18080'
npx playwright test --workers=1 --output '../../reports/workbench-2026-11-09/final-browser'
```

未设置 `WORKBENCH_LIVE_API` 时真实集成文件会明确跳过，因此日常模拟浏览器通过不能替代本轮实际 API 与模型验收。

## 验收覆盖

| 用例 | 自动化与集成证据 |
|---|---|
| R-01–09 | PlanReviewPanel 主测试、独立 Safety 测试；浏览器验证 Escape、遮罩、版本切换、保存后面板保留，并断言保存返回的新编号才被校验；真实后端保存为 r2、批准并只执行 r2 一次 |
| S-01–04 | runtime/GraphPanel 状态组合测试；浏览器从计划库打开运行计划，统计实际 EventSource 生命周期与零执行请求；真实 48 秒计划刷新、切新会话再返回自动接续 |
| S-05–09 | planObservation、history hydrator、reconnect 和 runtime 测试；revision 并发与 stream 后端测试；实际 HTTP SSE 浏览器服务验证 fresh ticket、after_seq、断线、重复聚焦、切计划及重连计时器取消 |
| L-01–04、06–08 | 1440×900、1366×768、1280/1279×768、1024×768/500、768/767×768、390×844、320×640、844×390 几何截图；拖动/键盘边界、旧缓存、长中文标题、抽屉焦点与 axe |
| L-05 | 1152×720 和 960×600 对应 1440×900 窗口的 125%/150% 有效 CSS 视口；横屏及带偏移 visualViewport 模拟。未以此声称已人工操作原生浏览器缩放菜单或真实手机软键盘 |
| P-01–04 | 引用草稿、会话切换、发送快照、失败保留和普通 chat/实验 plan 分流组件测试；实际多页 PDF 浏览器选择第 2 页原文、保留原问题、发送与连续对话 |
| P-05 | 后端文档来源、越权/越界/长度、稀疏页、提取状态、预算和多轮恢复测试；共享 API Unicode 与非法页码序列化测试 |
| P-06–09 | 阅读/执行图/聊天不重建实例、持久页码/缩放、身份首帧屏蔽、旧请求取消、StrictMode、翻译启动与轮询/逆序响应测试；手机/短屏浏览器和真实刷新恢复 |

## 关键发现及修正

验收期间修复了手机长标题将画布压为零高度、原生 dialog 的 Tab 边界、软键盘可视区域偏移、短阅读区翻译浮层无可读正文、划词菜单不能直接建立引用，以及 PDF 初始化页事件覆盖持久化页码等实际问题。首页 smoke 原有品牌文字和“对话”全局选择器随当前界面更新，仍保留溢出与浏览器错误断言；可选桌面壳和服务状态在 shell smoke 中显式模拟。

最终集成复核还修正了计划库打开请求乱序覆盖后选计划、同一计划进度刷新强制把手机对话切回执行图，以及旧 Shell 状态推导通知在恢复历史时误报完成。现在导航请求有代次/身份/会话检查，同计划快照刷新保留当前主区，桌面完成通知只由当前观察流程去重后的实时终态触发。

最初的长运行验收图使用多个同名 `tool_result` 产物，被调度器的产物唯一性规则拒绝；改用一条 48 秒任务验证同样的恢复链路，没有更改产物校验规则。真实 chat 首次被缺失的模型上下文容量配置拒绝，补齐隔离验证环境后继续执行；未绕过模型预算检查。

最终整套浏览器运行中，真实 PDF 用例在填写输入框前保存了选区坐标，页面移动后鼠标未命中文字。修正测试为先完成输入、等待文字层实际命中点、再取坐标选字，定点重跑通过；其余 36 个已通过的用例未重复执行。真实回答包含第 2 页的 `42.73` 及相邻第 3 页的 `19.26`，两次请求都走 chat、没有 plan，请求页码和刷新后第 2 页均有断言。

## 已知边界

- 当前 desktop bridge 没有提供可取消的系统窗口关闭回调。浏览器 beforeunload 与应用内离开保护已实现，未声称保护桌面系统关闭、强制结束进程或崩溃。
- 审批与执行仍是两个服务端请求；失败或响应不明分别核对，未提供原子性或网络恰好一次交付承诺。
- 其他客户端启动未观察的待运行/终态计划，在窗口聚焦或显式刷新时发现；未承诺后台全库即时订阅。
- 选中文字标记为用户引用；后端读取的原文作为受限上下文，未自动把用户选文标为已核实逐字证据。
- Windows 全量 Go 测试含已有平台假设：symlink 权限、POSIX 文件权限、Git CRLF 和打开文件重命名。分别记录 Windows 结果与 Linux 全量回归，不能把 Windows 全量报告为通过。

## 截图与复核

代表性截图集中在 `reports/workbench-2026-11-09/screenshots/`，另保留每个尺寸的原始聊天/详情截图与网络计数 JSON。已目检宽屏停靠、1024×500 抽屉、390px 输入区、320px 全屏详情、手机 PDF 刷新和真实连续回答；几何断言与 axe 同时通过，不仅依据截图或 axe 单独判断。

| 场景 | 截图 |
|---|---|
| 1440×900 / 1366×768 详情 | [1440](../../reports/workbench-2026-11-09/screenshots/details-1440x900.png) · [1366](../../reports/workbench-2026-11-09/screenshots/details-1366x768.png) |
| 中屏短窗口抽屉 | [1024×500](../../reports/workbench-2026-11-09/screenshots/details-1024x500.png) |
| 手机输入与全屏详情 | [390px 对话](../../reports/workbench-2026-11-09/screenshots/chat-390x844.png) · [320px 详情](../../reports/workbench-2026-11-09/screenshots/details-320x640.png) |
| 缩放等效 CSS 视口与横屏 | [125%](../../reports/workbench-2026-11-09/screenshots/details-1152x720.png) · [150%](../../reports/workbench-2026-11-09/screenshots/details-960x600.png) · [横屏](../../reports/workbench-2026-11-09/screenshots/chat-844x390.png) |
| 保存后仍在审阅 / 自动接续 | [审阅](../../reports/workbench-2026-11-09/screenshots/review-saved-open.png) · [观察](../../reports/workbench-2026-11-09/screenshots/observation-restored.png) |
| 真实执行刷新前 / 返回会话后完成 | [运行中](../../reports/workbench-2026-11-09/screenshots/live-running-before-refresh.png) · [完成](../../reports/workbench-2026-11-09/screenshots/live-completed-after-restoration.png) |
| 手机和中屏 PDF 对话 | [390px](../../reports/workbench-2026-11-09/screenshots/pdf-chat-answer-390x844.png) · [1024×500](../../reports/workbench-2026-11-09/screenshots/pdf-chat-answer-1024x500.png) |
| PDF 刷新页码与缩放 | [390px 第 2 页](../../reports/workbench-2026-11-09/screenshots/pdf-refreshed-page2-390x844.png) · [1024 第 2 页](../../reports/workbench-2026-11-09/screenshots/pdf-refreshed-page2-1024x500.png) |
| 真实模型的原文与相邻页连续回答 | [连续对话](../../reports/workbench-2026-11-09/screenshots/live-pdf-continuous-chat.png) |

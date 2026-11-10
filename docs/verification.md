# 质量与验证状态

本文件记录最近一次可复核的构建、测试与已知缺陷。原始证据默认写入 `reports/`（`.gitignore` 只保留 `reports/README.md` 与 `reports/README.en.md`），不随仓库提交。

## 最近验证

- 日期：2026-11-09（Asia/Shanghai）
- 代码状态：`feature/workbennch` 分支实现，基线 `e6bfb26`；2026-11-10 整理为 GitHub PR，此前 MyGo 桌面验证作为历史记录保留
- 环境：Windows 本机（Go 1.27.3、Node 24.14.0、Chrome）+ `golang:1.27.3-bookworm` 隔离容器；静态前端预览与独立真实 API/模型服务

| 层级 | 覆盖 | 结果 |
|---|---|---|
| 2026-11-09 工作台交互改进 UI-01–04 | 审阅草稿与 revision 执行、历史/SSE 恢复与运行控制、容器布局/原生抽屉、PDF 引用与连续对话及资源生命周期 | 前端 build/lint/bundle 通过，21 单元 + 486 组件测试通过；37 个浏览器用例最终均通过（整套 36 + 修正坐标等待后的真实 PDF 定点 1）；真实 48 秒任务保存并执行 r2、刷新/切会话自动接续且只 execute 一次；真实三页 PDF 两轮回答含第 2 页 42.73 与相邻第 3 页 19.26，0 plan，刷新保留第 2 页；Linux backend 全量 test/vet 通过，Windows API race 通过，Windows 全量仍有现有平台限制。截图、命令、具体覆盖与限制见 [实施报告](design/2026-11-09-workbench-implementation-report.md)。原生桌面系统关闭未有可取消 bridge，125%/150% 与软键盘以等效 CSS 视口/visualViewport 模拟记录 |
| 2026-11-02 MyGo 桌面外壳 | `desktop/main_mygo.go`、`desktop/mygo` 适配层、`mygo.Bind`、loopback proxy、托盘/通知/单实例/NSIS 安装包 | `go test ./...`、`go vet ./...`（desktop）通过；Windows `mygo build` 产出 18.6 MB exe 与 20 MB NSIS 安装包；安装包在 `Downloads` 目录静默安装验证通过，交互式 MUI2 向导提供选择安装位置/组件页面（从受 ACL 限制的工作区目录直接运行会被 NSIS 临时文件检查拒绝，属于工作区 ACL 限制）；真实启动验证窗口、前端、`/api/health` 与 bridge（NeedsOnboarding/GetRuntimeSettings/ExportDiagnostics/Notify）；supervisor 以 `CREATE_NO_WINDOW` 启动 backend/sandbox，运行时不再出现控制台窗口；关闭到托盘与第二实例唤起通过；Wails `-tags "desktop wails"` 与 Linux 交叉编译通过；前端 37 文件/146 tests + build + lint + bundle 通过。Windows 本机 backend/docker-sandbox 的失败项均为平台限制（symlink 权限、POSIX 文件权限、PDF OCR 外部工具、Windows 文件锁）；Linux 容器（golang:1.27.3-bookworm）内 backend/docker-sandbox/desktop 全量测试与 go vet 通过 |
| 2026-11-02 文档与架构图 | 全量 README 中英双语重写；3 张 draw.io/SVG + 2 张 Archify HTML 重建 | 74 个 README 文件的本地链接检查 0 缺失、代码围栏全配对；3 张 draw.io XML 解析通过；Archify `finalize` 的 validate/deliver/check/browser-check 四门禁通过（architecture / workflow） |
| 2026-11-01 记忆系统 | internal/memory + session + chat 多轮 + plan/任务注入 + consolidation + 前端 MemoryPanel | 容器内 `go build ./...`、`go vet ./...`、`go test ./...` 通过；前端 36 文件/144 tests + build + lint 通过；长对话 E2E 32 请求/24 消息上限/摘要生效；计划 A→B parent_plan_id 通过；真实 LLM（deepseek-v4.1-flash）15 轮基础 E2E 与 30 轮科研长任务 + 15 节点计划 A/B 通过；长任务发现并修复 createPlan nil pointer；详见 [MEMORY-TEST-REPORT](improvement-2026-11-01/MEMORY-TEST-REPORT.md) |
| 后端构建/单测 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过（第八轮新增 api/budget/project/usage/scheduler 用例全绿） |
| 后端 race | agent/conversation、prompts、planner、api、literature、documents、scheduler、modelprofile、modelresolver | 通过 |
| Docker Sandbox | `go build ./...`、`go test ./...` | 通过 |
| Docker Core | `go build ./...`、`go test ./...` | 通过（容器内 6 个含测试包） |
| 前端 | `npm test`、`tsc -b`、`vite build`、`npm run lint`、`npm run check:bundle` | 30 个测试文件 / 128 个测试通过；构建通过；`eslint .` 通过（本轮新增工具/Agent 设置页与 tool_call 节点展示） |
| Intent Recognition | `python -m unittest test_service.py -v` | 8/8 通过 |
| Research Optimizer | `python -m unittest discover -s tests -p 'test_*.py'` | 8/8 通过 |
| 离线示例 | paper-reproduction、autoresearch/intent_router | 2/2 通过 |
| Tool 模块 | `go build ./...` | 通过（无测试文件） |
| 既有 E2E | http-smoke、routing-and-prompts、literature、literature-plan、document-intelligence、model-profiles、verification、sandbox-network、round6 五套 | 全部通过（第八轮复跑 http-smoke） |
| 第八轮专项 E2E | `plan-library.mjs`、`repro-bundle.mjs`、`cost-budget.mjs`、`budget-recovery.mjs` | 全部通过 |
| 第九轮专项 E2E | `plan-revisions.mjs`（A1/A2/A3/A5）、`review-hitl.mjs`（A4/A6/A7/A8/A9） | 全部通过 |
| 第九轮真实浏览器 | Chrome 手工验收：审阅面板、revision 列表、节点 inspector、diff、保存草稿、批准并运行、收件箱 | 通过（截图见 reports/n05-*.png） |
| 第九轮回归 | `http-smoke`、`plan-library`、`repro-bundle`、`cost-budget`、`budget-recovery`、`routing-and-prompts` | 全部通过 |
| 第十轮复核 | 独立复核 N-01/N-02/N-03/N-05；修复 review gate 绕过、revision 绑定、manifest revision、禁用节点、artifact 脱敏、单任务预算等 12 类问题 | 后端/前端全量通过；六套专项 E2E 通过；详见 [FIX-REPORT-ROUND10](improvement-2026-10-26/FIX-REPORT-ROUND10.md) |
| 第十一轮 N-13 | 真 AllowedTools 白名单、ToolRegistry/ToolInvoker、native/HTTP/MCP stdio/MCP HTTP、tool_call 审批、ToolCallRecord 审计/回放、AgentProfile/PromptVersion、设置页工具/Agent tab | 后端全量通过；前端 128 tests + build + lint 通过；`tool-ecosystem.mjs` 44/44；详见 [FIX-REPORT-ROUND11](improvement-2026-10-26/FIX-REPORT-ROUND11.md) |
| 最终收口 | 13 套 E2E + backend/docker-sandbox/desktop/前端全量 | 13 套 E2E 全部 exit 0；backend/docker-sandbox/desktop 全量与 Windows 交叉编译通过；前端 36 文件/144 tests + build + lint；残余为 Windows 真机/签名/真实服务环境边界；详见 [FIX-REPORT-ROUND26](improvement-2026-10-26/FIX-REPORT-ROUND26.md) |
| 第二十五轮 运行时缓存清理 | CleanRuntimeCache + 清理链路 + UI | docker-sandbox/desktop 容器测试与 Windows 交叉编译通过；前端 36 文件/144 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND25](improvement-2026-10-26/FIX-REPORT-ROUND25.md) |
| 第二十四轮 NSIS 版本/烟测 | installer VERSION 参数、makensis 容器编译 | Debian+nsis 编译通过；backend 全量通过；前端 36 文件/144 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND24](improvement-2026-10-26/FIX-REPORT-ROUND24.md) |
| 第二十三轮 更新提示/系统包门禁 | 更新可用 banner、SystemPackages 门禁 | backend 全量通过；前端 36 文件/144 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND23](improvement-2026-10-26/FIX-REPORT-ROUND23.md) |
| 第二十二轮 安装进度 | RuntimeManager 阶段进度、native 安装状态、RuntimePanel 轮询 | docker-sandbox/desktop 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND22](improvement-2026-10-26/FIX-REPORT-ROUND22.md) |
| 第二十一轮 运行时安装/Docker 提示 | native InstallRuntime + sandbox/shell/bridge/UI、Docker 升级提示、阶段状态修正 | docker-sandbox/desktop 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND21](improvement-2026-10-26/FIX-REPORT-ROUND21.md) |
| 第二十轮 设置/运行时状态 | provider 持久化、native RuntimeStatus、诊断/日志入口 | backend/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 五套通过；详见 [FIX-REPORT-ROUND20](improvement-2026-10-26/FIX-REPORT-ROUND20.md) |
| 第十九轮 崩溃转储/打包/E2E | WER LocalDumps 注册、package.ps1、全量既有 E2E 回归 | backend/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；既有 E2E 13 套通过 + 环境限制项；详见 [FIX-REPORT-ROUND19](improvement-2026-10-26/FIX-REPORT-ROUND19.md) |
| 第十八轮 VM/安装 | VM Provider 可测试化 + 伪 wsl 集成、更新安装闭环、NSIS 可选 PDF 关联 | backend/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 五套 + vm fake-wsl 集成测试通过；详见 [FIX-REPORT-ROUND18](improvement-2026-10-26/FIX-REPORT-ROUND18.md) |
| 第十七轮 更新/remote/审计 | 自动更新闭环、remote mock 集成测试、导出审计、Windows 回归脚本 | backend/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 五套（含 repro-bundle）+ remote 集成测试通过；详见 [FIX-REPORT-ROUND17](improvement-2026-10-26/FIX-REPORT-ROUND17.md) |
| 第十六轮 导出/门禁/对话框 | 导出 provider 分支、GPU/Python 门禁、原生对话框、签名脚手架 | backend/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 四套 + repro-bundle 通过；详见 [FIX-REPORT-ROUND16](improvement-2026-10-26/FIX-REPORT-ROUND16.md) |
| 第十五轮 vm/门禁/指标 | vm WSL2 执行路径、规划器 capability 快照与 image_prepare→runtime_probe、/desktop/metrics、DesktopStatusBanner | 后端/desktop/docker-sandbox 容器测试与 Windows 交叉编译通过；前端 35 文件/141 tests + build + lint；E2E 全部通过；详见 [FIX-REPORT-ROUND15](improvement-2026-10-26/FIX-REPORT-ROUND15.md) |
| 第十四轮 P2/P4 | AppContainer 双 profile 网络、vm/remote Provider、诊断包崩溃转储、systray 托盘 | docker-sandbox/backend/desktop 容器测试与 Windows 交叉编译通过；vm/remote health 实测通过；前端 138 tests + build + lint 通过；http-smoke/desktop-smoke/sandbox-command/sandbox-network 全部通过；详见 [FIX-REPORT-ROUND14](improvement-2026-10-26/FIX-REPORT-ROUND14.md) |
| 第十三轮桌面产品化 | shell 编排/错误页/崩溃重启、Wails bridge、首次启动向导、诊断包、Ed25519 更新包、NSIS 静默安装；native AppContainer/lowil/Job 限额/探针与 runtime_hash | desktop/backend/docker-sandbox 容器全量测试通过；三模块 Windows 交叉编译（含 Wails tag）通过；前端 34 文件/138 tests + build + lint 通过；http-smoke 17/17、desktop-smoke 6/6、sandbox-command 8/8、sandbox-network 8/8；详见 [FIX-REPORT-ROUND13](improvement-2026-10-26/FIX-REPORT-ROUND13.md) |
| 第十二轮 Windows 桌面 | 跨平台 procgroup/sysinfo、LOG_DIR/APP_DATA_DIR/RA_CONFIG_PATH、日志轮转、代理保留、Command argv 契约、Provider 抽象、native uv/venv 骨架、desktop supervisor/proxy/settings、运行模式页与环境徽章 | 后端与 docker-sandbox 容器全量测试通过；desktop 单测与三模块 Windows 交叉编译通过；前端 131 tests + build + lint 通过；compose 栈重建后 http-smoke 17/17、desktop-smoke 6/6；详见 [FIX-REPORT-ROUND12](improvement-2026-10-26/FIX-REPORT-ROUND12.md) |
| Round7 深度测试 | deep API、真实 LLM 场景、任务执行/取消、文档 assist、sandbox 命令、AutoResearch、论文复现 | deep API 58/59；真实场景 18/18；AutoResearch 8/8 节点且 hidden holdout 3/3；论文复现 14/15 节点 |
| 前端 Playwright | 11 个测试 | 10 passed / 1 failed |

## LLM 计费暂停（已解除，2026-10-29 17:26）

- 2026-10-29 上午容器内 LLM provider 返回 403："分组「高速满血v4.1f-1017 #1038」的计费倍率已由 0.05 上调为 0.07，请求已暂停（未扣费）"。
- 用户确认新倍率后已解除；复测结果：
  - 真实 LLM 计划执行（7 节点代码执行 + 绘图 + 视觉校验）：completed，生成 2970×1768 PNG，visual 校验 pass（置信度 0.95）。
  - Round7 真实场景（code 3/3、plot 7/7、paper 4/4、文献 5/5）：全部通过。
  - N-05 专项与既有回归：全部通过。
- 详见 [FIX-REPORT-ROUND9.md](improvement-2026-10-26/FIX-REPORT-ROUND9.md) 第 7 节。

## 已知问题

以下缺陷来自 `reports/eval-2026-10-28/REPORT.md`；状态按当前工作区更新。

| ID | 严重度 | 问题 | 状态 |
|---|---|---|---|
| D1 | P1 | 论文复现的 claim rubric JSON 解码失败（`decode claim rubric: invalid character '#'`），阻塞 claim evidence | 待修复 |
| D2 | P1 | 论文元数据抽取错误（标题/作者） | 待修复 |
| D3 | P1 | intent 服务 `/predict` 偶发 502/超时，拖慢计划创建 | 待修复 |
| D4 | P2 | document assist 非法 `mode` 返回 502，应为 400 | 待修复 |
| D5 | P2 | 文献检索 relevance 排序差，跨 provider 结果不按相关性排序 | 已在工作区修复（见 [FIX-REPORT-LITERATURE-RELEVANCE.md](improvement-2026-10-26/FIX-REPORT-LITERATURE-RELEVANCE.md)），待提交 |
| D6 | P2 | AI 翻译浮层在 1024×500 遮挡 PDF 缩放按钮 | 已在本工作区修复；原 PDF 浏览器 6 项均通过，覆盖全文翻译收放、缩放、窄区隐藏/恢复、分隔条及偏移 visualViewport，见 2026-11-09 实施报告 |
| D7 | P3 | Playwright 选择器过期 | 已随 `5242ccd` 更新 |
| D8 | P3 | Paper_Reproduction 的 L4 agent planner 回退（`planner node ref is empty`） | 待修复 |
| D9 | P3 | intent `/health` 泄漏 provider/base_url/model | 待修复 |

## 复跑方式

后端与单元测试：

```bash
cd research-assistant/backend && go test ./... && go vet ./...
cd research-assistant/docker-sandbox && go test ./...
cd docker-core && go test ./...
cd research-assistant/frontend && npm test
```

容器内执行 Go 测试（固定工具链）：

```bash
bash research-assistant/scripts/container-go.sh "go test -count=1 ./..."
```

E2E 需要运行中的 Compose 栈。既有脚本在 `research-assistant/test/e2e/`，使用 `RA_API_URL` 与 `RA_API_TOKEN`：

```bash
RA_API_URL=http://127.0.0.1:8080 RA_API_TOKEN=... node research-assistant/test/e2e/http-smoke.mjs
```

部分套件需要额外环境：`document-intelligence` 使用 `PDF_OCR_MAX_PAGES=20`；`model-profiles`/`verification` 使用 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true`；E2E overlay 见 `research-assistant/test/docker-compose.e2e.yml`。

完整报告与日志（本地生成，不提交）：`reports/eval-2026-10-28/REPORT.md`、`reports/eval-2026-10-28/logs/`。

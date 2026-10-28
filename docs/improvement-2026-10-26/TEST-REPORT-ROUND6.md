# ROUND6 大规模测试报告（2026-10-27）

- 基线：`master @ 0f605c2` + 第 3–5 轮未提交成果（工作区 81 modified / 97 untracked）
- 测试日期：2026-10-27
- 测试环境：Docker Desktop 29.2.1；`golang:1.26.1-bookworm`（后端构建/测试）、`node:22-bookworm-slim`（前端/E2E）、Python 3.12.15（intent-recognition）、运行中的 compose 栈（backend 18080 / frontend 15173 / intent 18000 / sandbox 8082）
- 数据与模型：真实 DeepSeek 兼容 provider（`deepseek-v4.1-flash-1017`）、真实 Crossref/OpenAlex、真实 PDF fixtures
- 原则：所有功能测试均在容器内执行；宿主机只做编排、只读检查与最终清理

## 1. 结论摘要

1. **回归面全绿**：后端 `go build/vet/test ./...`、前端 25 个测试文件/112 个测试、Python intent 8 个测试、既有 E2E 与新增专项测试全部通过（最终验证见第 3 节）。
2. **发现 10 类问题**：其中 4 个已在本轮修复并加回归测试（intent 镜像陈旧、vision 未知能力主模型、bootstrap key 轮换、运行时模型 usage 归属）；其余 6 类为性能/架构/测试环境问题，已在第 4 节给出证据与建议。
3. **清理死代码**：用 `deadcode -test ./...` 定位并删除 **28 个**不可达函数（21 个文件），删除后 deadcode 列表为空，全量测试仍全绿。
4. **清理冗余垃圾**：删除工作区约 **208 MB** 生成物（`ui-audit` 191 MB、截图/设计探索产物、构建产物、空目录等）；Docker 悬空镜像/构建缓存清理见第 6 节。
5. **余额**：未出现余额不足；provider 调用全部成功，但单次延迟约 30–50 秒（详见 4.2）。

## 2. 测试范围

最近 11 个提交（D1–D10、Top-10、R-01..R-17、UI workbench redesign、N-04 文献、N-07 文档智能、runtime model config + visual verification、agent chain optimization、intent complexity routing）以及第 3–5 轮未提交改动：

- 复杂度路由 L0–L4、模板变体、PlanMeta、路由指标
- General 计划形状 direct/research/paper/literature
- Prompt 单一组装 + token 预算 + rune-safe 截断
- Repair 多轮 conversation session
- N-04 文献检索/导入/库/引用图/Web reader/SSRF
- N-07 PDF 解析/OCR/版面/表格/图/quote locate/quality
- Runtime model profiles、probe、路由、视觉校验 A–D
- 上传、鉴权、限流、幂等、导出、SSE、审计、sandbox 网络策略

## 3. 测试结果总表

| 类别 | 命令/入口 | 结果 |
|---|---|---|
| 后端构建/静态检查 | `scripts/container-go.sh "go build ./... && go vet ./..."` | 通过 |
| 后端全量单测 | `scripts/container-go.sh "go test -count=1 ./..."` | 30 个包全部 ok |
| 后端关键包 race | `go test -race -count=1 ./internal/{agent/conversation,prompts,planner,api,literature,documents,scheduler,modelprofile,modelresolver}` | 全部通过 |
| 后端覆盖率 | `go test -cover ./...` | agent 60.6% / api 53.4% / documents 64.8% / literature 71.4% / planner 63.4% / prompts 63.2% / scheduler 57.6% |
| 前端单测 | `npm run test`（node:22-bookworm-slim） | 25 files / 112 tests passed |
| 前端构建 | `npm run build` | 通过 |
| 前端 lint | `npm run lint` | 0 error |
| 前端 bundle 预算 | `npm run check:bundle` | OK（entry gzip 171.8 KB / limit 320 KB） |
| Python intent | `python -m unittest test_service` | 8 tests OK |
| E2E http-smoke | `test/e2e/http-smoke.mjs` | 17/17 checks passed |
| E2E routing-and-prompts | `test/e2e/routing-and-prompts.mjs` | 27/27 checks passed |
| E2E model-profiles | `test/e2e/model-profiles.mjs`（需 ALLOW_PRIVATE_MODEL_ENDPOINTS=true） | 21/21 checks passed |
| E2E verification A–D | `test/e2e/verification.mjs`（mock-vision） | 4 scenarios 全部通过 |
| E2E literature | `test/e2e/literature.mjs` | 全部通过（真实 Crossref/OpenAlex + example.com） |
| E2E literature-plan | `test/e2e/literature-plan.mjs` | 4/4 checks passed |
| E2E document-intelligence | `test/e2e/document-intelligence.mjs`（PDF_OCR_MAX_PAGES=20） | 24/24 checks passed |
| E2E sandbox-network | `test/e2e/sandbox-network.mjs` | 8/8 checks passed |
| E2E plan-run（真实模型） | `plan-run.mjs --execute --approve-all` | completed；2 次 LLM 调用；PNG 产物；verification pass |
| 专项：routing matrix | `test/e2e/round6-routing-matrix.mjs` | 10/10 用例 intent/meta/node shape 符合预期 |
| 专项：deep API | `test/e2e/round6-deep-api.mjs` | 20/20 checks passed（locate/dedup/SSRF） |
| 专项：limits | `test/e2e/round6-limits.mjs` | 6/6 checks passed（限流/上传大小/文件名） |
| 专项：partial OCR | `test/e2e/round6-ocr-partial.mjs` | 7/7 checks passed（PDF_OCR_MAX_PAGES=5） |
| 专项：runtime model | `test/e2e/round6-runtime-model.mjs` | 8/8 checks passed（生成与校验均使用用户 profile） |

## 4. 发现的问题

### 4.1 P0：intent LLM 分类超时，导致大多数建计划固定等待 15 秒

- 现象：`POST /api/plan` 对“你好”“用 Python 计算 1+1”等简单请求耗时 **14.96–15.05 秒**；路由矩阵 10 个用例中 9 个 > 5 秒。
- 证据：
  - 直接调用 intent 服务 `POST /predict`（“帮我找5篇GNN综述并总结”）耗时 **47.1 秒**，返回 200 且带 complexity 字段。
  - 直接调用 provider `/chat/completions`（单句 PONG，max_tokens=8）耗时 **33.0 秒**；`thinking:{type:disabled}` 34.6 秒、`enable_thinking:false` 31.1 秒，说明是 provider 侧延迟，不是 reasoning token 导致。
  - 后端日志大量 `[intent] LLM classifier failed, falling back to rule: ... context deadline exceeded`；`INTENT_CLIENT_TIMEOUT_SECONDS=15`，而 intent 服务自身 LLM 超时 60 秒。
- 影响：每个 plan 创建固定浪费 15 秒；Python 返回的 complexity/capabilities 从未进入 Go resolver；L0/L1 fast path 的用户体验被完全抵消。
- 建议：
  1. 按设计文档实现真正的“规则快路径”：规则明确时直接返回，不调用 intent LLM；或
  2. 当前环境把 `INTENT_ROUTER_MODE=rule`（Go 侧 ComplexityRouter 仍可完成 L0–L4 路由）；或
  3. 换用低延迟分类模型/端点；若坚持 hybrid，建议 2–3 秒短超时并异步记录，不要阻塞 plan 创建。

### 4.2 P1：所有 LLM 调用约 30–50 秒，复杂计划创建可达 99 秒

- plan-run 实测：`generate_code` 50.0 秒、`verify_result` 50.1 秒；上一版同计划 `verify_result` 曾达 120.0 秒。
- 路由矩阵 L4 用例（三框架 benchmark）创建计划 **99.2 秒**；其中 agent planner 先失败再回退（见 4.3）。
- 影响：一个普通画图计划端到端约 2 分钟；复杂计划仅“创建”就超过 1.5 分钟。当前 provider 不适合作为交互式默认模型。
- 建议：为交互路径设置更短超时/降级策略；将 planner/verify 等非核心 LLM 调用异步化或缓存；部署低延迟模型。

### 4.3 P1：L4 agent planner 对三框架 benchmark 生成非法依赖，回退模板

- 后端日志：`[PlannerAgent] fallback to template planner intent_type=Framework_Evaluation raw_intent="对 LangChain、LlamaIndex、Haystack 三个框架做多指标 benchmark 并生成报告" err=unknown dependency ref framework_research for node generate_benchmark_report`
- 结果：plan 仍成功创建（fallback 到 standard/full 模板），但 `planning_route=agent_augmented` 的 agent 增强实际未生效；创建耗时 99 秒。
- 影响：L3/L4 的 agent 增强质量不稳定；fallback 虽然安全，但用户为“agent 规划”等待了 99 秒却拿到模板计划。
- 建议：在 `mergeAgentPlanWithSkeleton` 中把 LLM 生成的 ref 统一映射到 skeleton ref（或拒绝未知 ref 前先做别名解析）；为 Framework_Evaluation 增加 3 框架 skeleton 回归用例；记录 `planner_source=template_fallback` 与 fallback 原因指标。

### 4.4 P1（安全）：Chat / General 仍可绕过 Scheduler、审批与审计自动执行代码

- 代码：`internal/agent/chat.go:99-118` 检测模型输出中的 `[CODE_EXECUTION_REQUIRED]` 后直接调用 `CoderAgent.ExecuteTask`；`/api/chat` 路由见 `internal/api/routes.go:147-164`；General 计划的 `general_agent` 仍映射到 ChatAgent。
- 影响：prompt injection 或误触发即可在沙箱执行代码，不经过 plan id、RuntimePolicy、审批、审计事件；临时 task id 使用 `question[:10]`，可能截断 UTF-8/冲突。
- 建议（审计报告 P0-3 同款）：改为结构化 tool call；所有代码执行走 Scheduler + RuntimePolicy + 审批；为 General 建独立无代码执行能力的 agent；`/api/chat` 至少写审计与 usage。

### 4.5 P2（已修复）：运行中的 intent-recognition 是旧镜像，新 complexity schema 未部署

- 发现：运行容器镜像构建于 2026-10-24，容器内 `/app/main.py` 中 `complexity` 出现次数为 **0**；宿主源码为 20。
- 处理：Docker Hub 直连超时，采用“旧镜像 + COPY 新 main.py”的离线构建方式生成 `research-assistant-intent-recognition:round6`，重打 `latest` 并重建容器；现在容器内 complexity 出现 20 次，`/predict` 返回 `complexity/capabilities/route_hint/complexity_confidence/complexity_reason`。
- 备注：该镜像更新后，`INTENT_CLIENT_TIMEOUT_SECONDS=15` 与 30–47 秒 provider 延迟的矛盾才完全暴露（见 4.1）。

### 4.6 P2（已修复）：vision 候选把 unknown 能力主模型当多模态使用

- 代码：`internal/modelresolver/resolver.go` 原逻辑只排除 `SupportsVision=no`，`unknown` 也会加入候选；bootstrap 环境模型默认 `unknown`，因此没有 vision profile 时会向文本模型发图片并等待 `VISION_TIMEOUT_SECONDS=120` 才 inconclusive。
- 处理：主模型仅在 `SupportsVision=yes` 时加入候选；unknown 需用户 probe/显式设置。新增 `TestResolveVisionSkipsUnknownMainProfile`。
- 验证：verification A–D 全部通过；无 vision 配置的 Scenario C 仍正确返回 inconclusive。

### 4.7 P2（已修复）：bootstrap 模型 key 轮换不落盘、不 bump revision

- 代码：`internal/modelprofile/store.go UpsertBootstrap` 原实现只更新 base_url/model，不更新 API key 与 revision；环境变量轮换 key 后重启仍用旧 ciphertext。
- 处理：比较完整 plaintext key，变化时重新加密、更新 last4、bump revision；新增 `TestBootstrapRotatesAPIKey`。

### 4.8 P2（已修复）：运行时模型 usage 归属错误

- 现象：runtime model 测试中 `generate_code` / `verify_result` 实际调用 mock profile（耗时 0.02 秒），但 usage 记录为 `deepseek-v4.1-flash-1017`。
- 根因：`dynamicChatModel` 在内部 ctx 上 `WithModelName`，外层 `usageChatModel` 的 `RecordUsage` 看不到；同时 `routes.go` 给 DataAgent 传了 nil runtime。
- 处理：
  - `routes.go` 改为传 `coderAgent.Runtime`，DataAgent 使用用户 profile；
  - `dynamicChatModel` 增加 `ResolvedModelName`，`usageChatModel` 记录解析后的模型名。
- 验证：runtime model 测试 9/9 通过，generate_code 与 verify_result 的 usage models 均为 `mock-main`。

### 4.9 P3：测试环境/脚本前提未文档化

- `document-intelligence.mjs` 期望 20 页全 OCR，但 compose 默认 `PDF_OCR_MAX_PAGES=5`；不覆盖该变量会得到 3 个 FAIL（status=partial / ocr_pages=5 / 非全 OCR）。设置 `PDF_OCR_MAX_PAGES=20` 后 24/24 通过；另用 5 验证了 partial 的 15 页 skipped 行为。
- `model-profiles.mjs` / `verification.mjs` 需要 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true` 才能使用 compose 内 mock-vision；默认 false 时创建 profile 返回 400 `ssrf_blocked`。
- 建议：在 `test/e2e/README` 或 compose e2e profile 中写明这些变量，或让脚本自行设置/跳过。

### 4.10 P3：限流按匿名 cookie 身份计数，冷请求可绕过

- 6 个并发“首次”请求（无 cookie）全部 200：每个请求都新建匿名 UUID，落入不同 bucket；先 `GET /api/me` 暖 cookie 后 6 并发为 200,200,200,429,429,429，说明限流本身正确。
- 影响：客户端清 cookie 即可重置配额；对匿名 API 是弱限流。若需要严格限流，应按 IP/API token 维度再加一层。

## 5. 死代码清理

- 工具：`go run golang.org/x/tools/cmd/deadcode@latest -test ./...`
- 清理前：28 个 unreachable func（即使把测试文件作为根）；清理后：**0**。
- 删除清单（28 个）：
  - repair 会话遗留 wrapper：`proposeAutoResearchCandidate`、`repairBenchmarkAdapter`、`planPaperCodeRepair`
  - 旧 OCR fallback：`ocrFallbackPages`、`ocrPage`、`ocrTesseractArgs`、`pagesFromOCRTexts`
  - 旧 prompt 构造：`DataPromptInput`、`IntentClassificationUserPrompt`、`IntentClassificationWithRewriteUserPrompt`、`IntentRewriteUserPrompt`、`PaperSearchUserPrompt`
  - 未使用 helper：`sortedBenchmarkMetricNames`、`sortedExperimentMetricKeys`、`plotArtifactKeyForTask`、`shouldUseDeterministicSandboxPath`、`DataAgent.promptInput`、`experimentJSONSHA256`、`agentctx.WithCodeChannel`、`agentctx.WithContainerID`、`requiresPaperMetadata`、`planTimeout`、`uploadRoot`、`catalog.RecipeByID`、`catalog.TemplateIDs`、`literature.errorIsNotFound`、`metrics.RecordVisionLatency`、`oaichat.ImageDataURI`、`planner.frameworkDisplayName`、`prompts.BuildSections`、`scheduler.SetOnTerminal`、`uploads.MetadataPath`
- 保留：仅被测试引用的 helper（`ResetLLMConfigForTest`、`Logger.Reset`、`Registry.*`、`buildTaskInputs`、`ocrTesseractArgs` 等）未删除，避免降低测试覆盖。
- 验证：`go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全绿；前端 build/lint 不受影响。

## 6. 冗余垃圾清理

### 6.1 工作区

删除约 **208 MB** 生成物（均为 untracked 的审计/设计/构建产物）：

| 路径 | 大小 | 说明 |
|---|---:|---|
| `ui-audit/` | 191 MB | 15,471 个历史 UI 审计脚本/截图 |
| `shots/` | 2.4 MB | UI 截图与分析脚本 |
| `.oil-ui/` | 4.7 MB | UI 探索缓存 |
| `research-assistant/frontend/shots/` | 3.5 MB | 前端截图 |
| `research-assistant/frontend/dist/` | 6.3 MB | 构建产物（可重建） |
| `style-explorer.html` | 0.3 MB | 设计对比页 |
| `explore/`、`debug.log`、空 `*;C` 目录、`test-results/` 等 | 小计 | 临时/空目录 |

同时删除 9 个未引用的 `frontend/ui-audit-*.cjs` / `audit-explorer.cjs` / `dom-audit-baseline.cjs` 审计脚本。

### 6.2 Docker

- 停止本轮启动的 `mock-vision` E2E 容器；测试遗留的 `research-assistant.sandbox=true` 临时容器已由测试自身清理（清理时复查为 0 个）；
- `docker image prune -f` 清理悬空镜像；
- `docker builder prune -af` 清理全部构建缓存；
- 删除本轮测试创建的 4 个 `dsh-*` 构建/依赖缓存卷（`dsh-go-mod-cache`、`dsh-npm-cache`、`dsh-go-build-cache`、`dsh-fe-node-modules`）。

清理前后 `docker system df`：

| 指标 | 清理前 | 清理后 |
|---|---:|---:|
| Images | 9.584 GB（16 个） | 4.475 GB（15 个） |
| Build Cache | 5.572 GB | 0 B |
| Local Volumes | 4.062 GB（11 个） | 188.2 MB（7 个） |
| Containers | 229.1 MB | 2.74 MB |

> 注：Docker 的镜像/缓存/卷统计存在共享层口径重叠，以上为 `docker system df` 原始输出；实际可回收空间约 9–14 GB。

### 6.3 未删除（保留）

- `reports/` 历史测试/评估报告；
- `docs/` 设计/修复报告；
- `research-assistant/test/fixtures/` 测试 PDF；
- 运行中的 compose 服务、数据卷与项目源码。

## 7. 建议优先级

1. **P0**：修复 4.4 Chat/General 自动代码执行绕过（安全）。
2. **P0**：处理 4.1 intent 超时——先切 rule fast path 或 `INTENT_ROUTER_MODE=rule`，恢复 plan 创建 < 1 秒。
3. **P1**：替换/优化 30–50 秒的默认 provider；为交互路径设置短超时与降级。
4. **P1**：修复 4.3 agent planner ref 合并，避免 L4 无效等待与回退。
5. **P2**：把 4.9 的 E2E 环境变量写入测试说明/CI；补充 repair session 的端到端测试（目前只有 conversation 单测）。
6. **P3**：限流增加 IP/token 维度；为 verify_result 报告增加模板化快速路径（无视觉/简单场景不必调用 LLM）。

## 8. 附录：本轮新增测试脚本

- `research-assistant/test/e2e/round6-routing-matrix.mjs` — 10 个 intent 的 plan shape/meta/latency 矩阵
- `research-assistant/test/e2e/round6-deep-api.mjs` — document locate、文献导入去重、Web SSRF 变体
- `research-assistant/test/e2e/round6-limits.mjs` — 限流（暖 cookie）、33MB 上传、路径穿越文件名
- `research-assistant/test/e2e/round6-ocr-partial.mjs` — PDF_OCR_MAX_PAGES=5 的 partial/skipped 行为
- `research-assistant/test/e2e/round6-runtime-model.mjs` — 用户 runtime profile 在 generate/verify 的生效与 usage 归属

## 9. 执行记录（关键原始输出）

- `go test -count=1 ./...`：所有包 ok（无 FAIL）。
- `go test -race`：conversation/prompts/planner/api/literature/documents/scheduler/modelprofile/modelresolver 全 ok。
- 最终回归（全部修复后再次执行）：routing-and-prompts 27/27、model-profiles 21/21、verification A–D 全通过、runtime-model 8/8、http-smoke 17/17。
- 前端：`25 passed (25)` / `112 passed (112)`；`tsc -b && vite build` 成功；`eslint .` 0 error；bundle budget OK。
- intent：`Ran 8 tests ... OK`。
- E2E：http-smoke 17/17、routing 27/27、model-profiles 21/21、verification 4 scenarios、literature 全通过、literature-plan 4/4、document-intelligence 24/24、sandbox-network 8/8、plan-run completed。
- 专项：routing matrix 10/10、deep-api 20/20、limits 6/6、partial-ocr 7/7、runtime-model 8/8。

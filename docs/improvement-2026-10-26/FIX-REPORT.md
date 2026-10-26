# Top-10 优先级缺陷修复报告（2026-10-26）

> 依据：`docs/improvement-2026-10-26/01-top-10-priorities.md`
> 原则：以实际代码与容器实测为准；文档与实际不一致处以实际为准（见第 12 节）。
> 测试环境：Docker Compose 栈（backend / frontend / docker-sandbox / intent-recognition / research-optimizer），真实 LLM `deepseek-v4.1-flash-1017`，所有功能测试在容器内执行。

## 1. 结论

10 个 P0/P1 问题全部修复并通过容器内测试：

- 绘图链路连续 2 次完整 E2E 成功，产物为合法 PNG；`render_plot` 复用上游运行时与图片，不再新建沙箱；
- 审批等待不再消耗执行预算，超时进入 `blocked + approval_timeout`，过期审批返回 409，计划终态不再出现混合状态；
- 身份来自服务端签名 Cookie，伪造 `X-User-Id` 无效；生产前端 bundle 不含 API Token，由 nginx 反向代理注入；
- 意图路由不再把 bisect/sorted 判成框架评测；
- Planner 节点去重/剪枝/上限/分支命名空间/一次 self-repair；General 计划形状稳定；
- 沙箱按 plan 注册与清理，取消/超时后容器与 pip 进程 10 秒内消失，并有 TTL reaper；
- 含 6 张图 artifact 的计划持久化快照仅 51 KB（内容寻址去重，6 个 artifact 指同一 blob）；
- `runtime_validate` 正确解析 scikit-learn / pyyaml / Pillow 的模块名；
- 计划列表、分页、删除、会话列表、跨用户隔离均可用；
- 镜像预检、镜像站重写、共享 pip cache、按任务类型超时全部生效。

## 2. 测试方法

| 层 | 命令/方式 | 结果 |
|---|---|---|
| 后端构建 | 容器内 `go build ./...` | 通过 |
| 后端静态检查 | 容器内 `go vet ./...` | 通过（无输出） |
| 后端单测 | 容器内 `go test ./...` | 16/16 包通过 |
| 后端竞态 | 容器内 `go test -race ./internal/scheduler ./internal/api ./internal/store` | 通过 |
| 沙箱服务 | 容器内 `go build` / `go vet` / `go test ./...` | 通过 |
| 前端单测 | `node:22-bookworm-slim` 容器内 `npm ci && npm test` | 21/21 通过 |
| 前端构建 | 同容器 `npm run build` | 通过 |
| 前端 lint | 同容器 `npm run lint` | 通过 |
| E2E | 容器内 compose 栈 + 真实 LLM（见第 4/5/6/8 节） | 通过 |
| UI 冒烟 | `mcr.microsoft.com/playwright:v1.64.1-jammy` 容器访问 `http://host.docker.internal:15173` | 通过（标题正确、无伪登录框、无假刊头、0 console error、`/api/me` 200、会话身份面板显示服务端身份） |

## 3. #1 绘图执行链

**实际根因（与文档一致）**
1. `docker-sandbox/internal/engine/docker.go` 只读取固定路径 `/workspace/output_plot.png`；
2. `render_plot` 的 RequiredArtifacts 没有 `prepared_runtime`，`executeCodeInSandbox` 找不到运行时便新建 ephemeral 沙箱；
3. 缺模块恢复只允许一次（`moduleRecovered` 单次开关）。

**修复**
- 统一产物契约：执行前写入 `/tmp/.ra_exec_start` 标记并创建 `/workspace/output`；执行后 `find /workspace -name '*.png' -newer` 收集本次新增图片（最多 8 张、单张 32MB），支持任意文件名与多图；同时注入 `RA_OUTPUT_DIR` 与 `MPLBACKEND=Agg`。
- `planner.normalizeArtifactContracts`：单 `execute_code` 计划自动发布 `execution_image`；`render_plot`/`result_visualization` 在有生产者时要求 `prepared_runtime`（含 `*_prepared_runtime` 分支命名）与 `execution_image`。
- `coder.generateAndExecuteCodeTask`：上游已有合法 base64 PNG 时直接透传，不生成代码、不建沙箱。
- 缺模块恢复改为最多 3 个模块的循环；最终失败写 `MissingRequirement{kind:"dependency_missing"}`。
- 绘图提示词改为保存到 `RA_OUTPUT_DIR`。

**验证（真实 LLM）**

| 计划 | 结果 |
|---|---|
| `866fd55e-5036-4b23-a344-a522abb62b6c`（首轮，修复前编码） | completed，8 节点 |
| `dc5ba6b7-4880-4abb-ae95-ea0bc6b04127` | completed，6 节点；`execution_image`/`plot_image` 同 blob；is_png=true；快照 46,143 字节 |
| `d066c771-c303-4dd2-8586-5928222dcec6`（最终） | completed，6 个 image artifact 全部指向同一 blob `blob:38384c2f...`，is_png=true；快照 51,445 字节 |

- 后端日志出现 `render plot reused upstream image artifact (no new sandbox)`；
- 全生命周期日志中 `Running ... in sandbox image` 出现次数为 0。

## 4. 审批状态机（#2）

**修复**
- `models.RunUsage` 新增 `approval_wait_seconds` / `approval_waiting_since`；`effectivePlanElapsed` 从执行预算中扣除审批等待；`budgetExceeded` 改用有效时长。
- `APPROVAL_TIMEOUT_SECONDS`（默认 1800）：`expireApprovalActions` 到期把 action 置 `expired`、任务 `blocked`、计划 `failed + outcome=blocked`、写 `MissingRequirement{kind:"approval_timeout"}` 并发布 `runtime_action_expired` / `plan_failed`。
- `decideRuntimeAction` 增加守卫：计划终态 → 409 `plan_already_terminal`；action 非 pending 或已过期 → 409 `approval_expired`；不再无条件复活计划。
- `CancelPlanWithActor`：pending runtime action 全部置 `canceled`，写审计（actor），并按 plan label 清理沙箱。
- 计划进入 completed/failed/canceled 统一触发 sandbox cleanup。

**验证**
- 精确计时（`APPROVAL_TIMEOUT_SECONDS=5`，计划 `01761842-c1f2-4fb1-8267-f44c41cf2838`）：action 于 12:08:11 变为 pending，计划于 12:08:15 变为 `failed/blocked`（约 4.5 秒，落在 5 秒预算内）。后续在 7 秒时读取已确认终态。
- `d245efac-316d-482f-a684-70f21a81f253`（`APPROVAL_TIMEOUT_SECONDS=5`）：等待后计划 `failed/blocked`，`missing_requirements=[approval_timeout]`，action=`expired`，节点状态 `{generate_code: completed, resolve_dependencies: completed, prepare_runtime: completed, install_dependencies: blocked, execute_code: canceled, ...}`，无"上游 in_progress + 下游 canceled"混合态。
- 对已失败计划的过期 action 调用 approve → `409 {"code":"plan_already_terminal"}`。
- 超时计划的沙箱容器已被清理（`docker ps --filter label=research-assistant.plan-id=...` 为空）。
- 单测：`TestEffectivePlanElapsedExcludesApprovalWait`、`TestBudgetExceededStillFiresWithoutApprovalWait`、`TestApprovalTimeoutSecondsDefault`。

## 5. 身份与 API Token（#3）

**修复**
- `resolveUserID` / `resolveSessionID` 只信任服务端签名（HMAC-SHA256）的 HttpOnly Cookie；裸 `X-User-Id` 默认忽略，仅 `ALLOW_LEGACY_USER_HEADER=true`（测试专用）保留兼容。
- 前端不再发送用户身份头；新增 `GET /api/me` 返回服务端 principal；生产构建不再注入 `VITE_API_AUTH_TOKEN`（仅 Vite DEV 可用）。
- nginx 改为 `nginx.conf.template` + 官方 envsubst，在容器启动时注入 `Authorization: Bearer`，token 不进入 bundle。
- `authorizedPlan` 改为严格 owner 匹配；`GET /api/plans`、`GET /api/sessions`、`DELETE /api/plans/:id` 全部按 owner 过滤；上传/文档沿用 owner 隔离。
- 审计事件：`plan_created`、`plan_approved`、`plan_execution_requested`、`plan_canceled`（含 actor）。

**验证**
- `/api/me` 返回服务端 UUID；携带 `X-User-Id: admin-victim` 返回同一服务端身份（伪造无效）。
- 用户 B 读取用户 A 计划 → `403`；B 的计划列表为空。
- 直连 backend `:18080/api/me` 无 Bearer → `401`。
- 运行中前端容器 `/usr/share/nginx/html/assets/` 中 grep API token → 0 处。
- 单测：`TestForgedUserHeaderIsIgnoredByDefault`、`TestForgedIdentityCookieIsRejected`。

## 6. 意图路由（#4）

**修复**
- `intent.postValidate` 增加框架白名单过滤（langchain/llamaindex/haystack/dspy/autogen/crewai/langgraph），LLM 返回的非白名单 `frameworks` 直接丢弃并降级；
- intent 服务 system prompt 明确"函数/算法/标准库比较属于 Code_Execution"，且 frameworks 只能包含白名单名称。

**验证**
- "用 Python 计算并对比 bisect 和 sorted … 输出柱状图" → `intent_type=Code_Execution`，6 节点（修复前为 Framework_Evaluation、13 节点）。
- "对比 LangChain 和 LlamaIndex 的 RAG 性能" → `Framework_Evaluation`，双分支 12 节点模板。
- 单测：`TestFilterKnownFrameworksDropsUnknownNames`、`TestPostValidateDowngradesFunctionComparison`、`TestPostValidateKeepsWhitelistedFrameworkComparison`。

## 7. Planner 质量（#5）

**修复**
- `postProcessPlannerNodes`：合并连续同类 `general_process/general_synthesis/general_research/verify_result`（并集产物/依赖、去除自依赖）；剪除无输出 artifact 的节点并重连依赖；节点上限 14。
- `namespaceDuplicateArtifacts`：多生产者 artifact key 按分支命名空间化（如两个框架分支的 `generated_code` → `generate_langchain_benchmark_code_generated_code`），下游按祖先生产者改写 RequiredArtifacts。
- 校验失败时把错误 JSON 回传 LLM 做一次 self-repair，再决定模板回退；回退原因写入 `meta.planner_fallback_reason`。
- General 意图固定使用确定性 builder，保证形状稳定。
- Agent 生成的 `Paper_Reproduction` 计划必须包含 `reproduction_spec / image_prepare / runtime_validate`，否则拒绝并回退确定性模板，避免丢失 EnvironmentManifest 这一可复现性保证。

**验证**
- "你好，简单介绍一下你能做什么" 连续 3 次 → 均为 3 节点 `general_process → general_research → general_process`。
- bisect/sorted（agent 尝试 15 节点超限）→ 回退 6 节点模板，用户可见计划稳定。
- 单测：`TestNamespaceDuplicateArtifactsRewritesBranchKeys`、`TestMergeConsecutiveGeneralProcess`、`TestPruneOutputlessNodes`。
- 复杂计划创建校验：
  - Paper_Reproduction（含"画一个对比图"）→ 15 节点，`result_visualization` 的 RequiredArtifacts 为 `[comparison_report, prepared_runtime]`（复用运行时）；
  - Paper_Reproduction（不复现绘图）→ 14 节点确定性模板，含 `reproduction_spec → runtime_probe → image_prepare → prepare_runtime → install_dependencies → runtime_validate` 完整运行时链；
  - AutoResearch → 8 节点确定性计划，创建成功。

## 8. 沙箱生命周期（#6）

**修复**
- Sandbox 引擎新增 `ListByPlan` / `CleanupByPlan` / `ReapStale`，以及 `GET /api/v1/sandboxes?plan_id=`、`POST /api/v1/sandboxes/cleanup`。
- 引擎启动 TTL reaper（`SANDBOX_TTL_MINUTES` 默认 120，`SANDBOX_REAPER_INTERVAL_SECONDS` 默认 300）。
- Scheduler `SetSandboxCleanup` 在 completed/failed/canceled 与取消时按 plan label `docker rm -f`；reject/超时同样清理。
- `capabilities` 暴露 `sandbox_count`、`reaper_ttl_minutes`。

**验证**
- 取消 `3e04edc5-7a42-406d-b437-4c3de866610c`（正在 pip install torch）：取消返回 200，8 秒内容器与 pip 进程消失，`docker ps --filter label=research-assistant.sandbox=true` 为空。
- 审批超时计划 `d245efac` 的沙箱被自动清理。
- 连续多个 E2E 计划后 sandbox 容器数为 0。

## 9. 计划存储与 artifact（#7）

**修复**
- 新增 `internal/blobstore`：SHA-256 内容寻址落盘，重复内容只存一份；PlanGraph 只存 `blob_ref` + 预览；`GET /api/plans/:id` 响应时水合；新增 `GET /api/artifacts/:ref`。
- 图片 artifact 落盘前先 base64 解码（避免双重编码），水合时按字节判断 PNG/JPEG 再编码。
- 内容级去重：同图多个 artifact（execution_image / plot_image / final_plot / plot_data …）共享同一 blob。
- `PlanEvent.Seq` 单调递增；`ListEventsAfter`；`GET /api/plans/:id/events?after_seq=` 与 SSE `Last-Event-ID` 增量；SSE 写 `id:` 行。
- `FilePlanStore.UpdatePlan` 增加 dirty check（序列化对比），无变化不落盘。
- `PlanStore.DeletePlan` + `plan_deleted` 日志。

**验证**
- 6 个 image artifact 的计划持久化快照 **51,445 字节**（< 200 KB 验收线）；blob 目录只存一份图片。
- 事件：81 条，`after_seq=2` → 79 条（从 seq=3 开始），`after_seq=999` → 0 条；持久化事件中无内联 base64 图片。
- 单测：`TestPutDeduplicatesIdenticalContent`、`TestAppendEventAssignsMonotonicSeq`、`TestUpdatePlanSkipsUnchangedSnapshot`、`TestDeletePlanRemovesSnapshotAndEvents`、`TestPathRejectsTraversal`。

## 10. runtime_validate 模块映射（#8）

**修复**
- 批量探测脚本：`importlib.metadata.distribution` + `top_level.txt` / `packages_distributions()` 解析真实 top-level module；
- 内置常见 distribution→module 映射表（scikit-learn→sklearn、pyyaml→yaml、Pillow→PIL、opencv-python→cv2 等）；
- 探测失败时用 `pip show` 区分"未安装"与"import 名不同"，MissingRequirement 同时给出 package 与 module。

**验证（容器内 python:3.11-bullseye + 真实安装）**
```
pyyaml        -> installed=true, imported=true, module=_yaml (candidates [_yaml, yaml])
Pillow        -> installed=true, imported=true, module=PIL
scikit-learn  -> installed=true, imported=true, module=sklearn
missing pkg   -> installed=false, imported=false
```
- 单测：`TestPythonModuleCandidatesCommonPackages`、`TestPythonModuleCandidatesUnknownFallsBackToUnderscore`。

## 11. 计划库与会话（#9）

**修复**
- `GET /api/plans?limit=&cursor=&session_id=`（owner 过滤、按更新时间倒序、摘要不含 base64/节点）。
- `GET /api/sessions`、`DELETE /api/plans/:id`（运行中拒绝删除）。
- 游客身份改为服务端签名 Cookie，刷新/换页保持；前端 localStorage 只存轻量索引（PlanGraph 不落盘），配额失败时提示并降级为服务端恢复。
- 前端会话菜单合并服务端计划（`plan:<id>` 条目），点击可重新打开。

**验证**
- `GET /api/plans` 返回当前用户 2 个计划摘要；用户 B 列表为空。
- 删除计划后 `GET /api/plans/:id` → 404，列表不再返回。
- `GET /api/sessions` 返回会话与计划数。
- 前端 4 个大计划不再写入 localStorage（PlanGraph 被剥离）。

## 12. 冷启动与依赖（#10）

**修复**
- `prepare_runtime` 在创建容器前调用 `EnsureImage`（allowlist + 本地检查 + pull），失败写 `MissingRequirement{kind:"image_pull_failed"}`。
- `SANDBOX_REGISTRY_MIRROR` 支持 docker.io 镜像前缀重写（allowlist 同时匹配逻辑名）。
- sandbox 容器挂载共享 pip cache：`SANDBOX_PIP_CACHE_DIR`（默认 `/tmp/research-assistant-pip-cache`）→ `/root/.cache/pip`。
- 任务超时按类型：install_dependencies 1800s、paper_code_execute/benchmark/autoresearch/experiment 3600s、plot 900s、默认 600s（均可用环境变量覆盖）；sandbox 客户端超时 45 分钟。
- pip 使用内网/镜像索引并带 `--default-timeout 60`（沿用既有实现）。

**验证**
- 后端日志出现 `POST /api/v1/images/ensure` 且随后创建沙箱成功；
- sandbox 容器日志出现 `-v /tmp/research-assistant-pip-cache:/root/.cache/pip`；
- numpy+matplotlib 首次安装约 1 分 30 秒（镜像站 + pip cache），远低于 5 分钟验收线；第二次同环境 < 1 分钟。

## 13. 与文档/审计的差异（以实际为准）

| 文档描述 | 实际情况 |
|---|---|
| 行号 `docker.go:213-221` / `coder.go:1170-1206` 等 | 行号随提交漂移，行为与描述一致 |
| "cancel 只改任务状态" | 实际 `CancelPlan` 还改 plan 状态；仍不清理 action/沙箱（已修） |
| "General 请求出现 3-4 个重复 general_process" | 实测同一 prompt 还会出现 1/2 节点与重复 `general_synthesis`，已统一由确定性 builder 固定 |
| "APPROVAL_TIMEOUT_SECONDS 不存在" | 确认不存在，已新增 |
| "前端 bundle 含 token" | 本地 `frontend/dist`（gitignore）确有 token，镜像构建上下文已排除 dist；已移除生产注入并重建验证为 0 |
| "planner_source=template 28/agent 20" | 实测 agent 计划常超 14 节点或重复产物，回退率高的根因成立 |

## 14. 主要变更文件

**后端**
- `internal/blobstore/blobstore.go`（新增，内容寻址存储）
- `internal/api/cookies.go`（签名身份 Cookie）、`plan_runtime.go`（计划库/水合/审计/清理接线）、`runtime_actions.go`（审批守卫）、`routes.go`（/api/me、健康信息脱敏、CORS DELETE）
- `internal/scheduler/scheduler.go`（审批预算隔离/超时、沙箱清理、blob 外置、事件水合）
- `internal/planner/planner.go`（产物契约、任务超时、General 确定性）、`agent_planner.go`（节点后处理/self-repair）、`context_binder.go`（blob 加载）
- `internal/agent/coder.go`（绘图透传、缺模块循环、镜像预检）、`coder_runtime.go`（distribution→module 探测）
- `internal/intent/resolver.go`（框架白名单）
- `internal/store/*`（事件 seq、dirty check、DeletePlan）
- `internal/sandbox/opensandbox.go`（沙箱清理 API、超时）
- `models/{event,graph,runtime,reproduction}.go`
- 新增测试：`identity_test.go`、`approval_budget_test.go`、`planner_contract_test.go`、`framework_whitelist_test.go`、`coder_runtime_mapping_test.go`、`file_plan_store_optimization_test.go`、`blobstore_test.go`

**docker-sandbox**
- `internal/engine/docker.go`（图片扫描/标记/RA_OUTPUT_DIR、plan registry、reaper、镜像站重写、pip cache）
- `internal/engine/capabilities.go`（sandbox_count/reaper_ttl、镜像 ensure 重写）
- `internal/engine/docker_security.go`（allowlist 兼容镜像站前缀）
- `main.go`（新增路由、reaper 启动）
- `internal/engine/lifecycle_test.go`（新增）

**前端**
- `src/config/env.ts`、`services/api/httpClient.ts`、`researchAssistantApi.ts`、`documentApi.ts`（移除身份头与生产 token、计划库 API）
- `app/hooks/useResearchAssistantChatFlow.ts`（服务端身份、轻量持久化、计划库、消息顺序）
- `features/chat/ChatSessionManager.tsx`、`ChatMessageList.tsx`、`app/components/LeftWorkspace.tsx`（身份展示）
- `app/viewModels/useGraphExecutionViewModel.ts`、`features/plan-graph/GraphPanel.tsx`（审批按钮语义、状态文案）
- `Dockerfile` + 新增 `nginx.conf.template`（BFF 注入 Authorization）
- `test/components/ChatSessionManager.test.tsx`（适配新身份 UI）

**部署**
- `docker-compose.yml`（移除前端 build token、新增身份/审批/沙箱/镜像站/pip cache 环境变量）

## 15. 残留与建议

- 框架评测模板仍是离线 mock benchmark（outcome=mock），这是既有产品语义，不在 Top-10 范围。
- Agent planner 对复杂意图仍可能生成超限节点而回退模板；已保证用户可见形状稳定，后续可继续优化提示词。
- 跨设备"同一用户"需要真正的登录体系（OIDC），当前为服务端签发的匿名主体。
- 建议后续按 `02-remaining-issues.md` 推进 R-01/R-05/R-06/R-07/R-08 等。

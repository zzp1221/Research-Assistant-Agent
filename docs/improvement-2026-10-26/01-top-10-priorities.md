# 最值得先修的 10 个点

> 来源：2026-10-26 对运行中容器栈（backend / frontend / docker-sandbox / research-optimizer / intent-recognition）的实测审计。
> 判定标准：用户可见阻断 > 安全与数据一致性 > 资源/成本失控 > 可复现性风险；同时考虑修复成本与对后续功能的阻塞程度。
> 说明：以下问题在本次审计中均**未修复**，按建议顺序处理。

## 速览

| # | 优先级 | 问题 | 直接影响 | 预估工作量 |
|---|---|---|---|---|
| 1 | P0 | 绘图执行链产物丢失、render_plot 重建沙箱 | 最常见的“画图”任务失败 | 2–3 人日 |
| 2 | P0 | 审批等待耗尽计划预算、过期审批可复活计划 | 计划状态错乱、无法安全审批 | 2–3 人日 |
| 3 | P0 | X-User-Id 可伪造 + API Token 进前端 bundle | 跨用户读写/审批计划 | 3–5 人日 |
| 4 | P1 | 意图路由把代码对比判成框架评测 | 生成错误计划、浪费 LLM/算力 | 1–2 人日 |
| 5 | P1 | Planner 回退率高、节点重复 | 计划质量不稳定、延迟高 | 3–5 人日 |
| 6 | P1 | 沙箱容器泄漏、取消不杀进程 | 资源耗尽、成本失控 | 2–3 人日 |
| 7 | P1 | 计划存储写放大、图片重复、SSE 全量重放 | 计划越大越慢、磁盘膨胀 | 5–8 人日 |
| 8 | P1 | runtime_validate 包名→模块名映射错误 | 论文复现误报失败 | 0.5–1 人日 |
| 9 | P1 | 无计划列表、游客刷新丢计划、localStorage 溢出 | 计划无法找回、会话丢失 | 3–5 人日 |
| 10 | P1 | 镜像预检缺失、依赖安装慢且重复 | 冷启动失败、单任务 10+ 分钟 | 3–5 人日 |

---

## 1. 绘图执行链：产物丢失 + render_plot 重建沙箱

### 问题出在哪

- 实测 plan `a1678d3c-ff54-40ef-a2a1-d4c9cdd83e17`（“用 Python 画正弦/余弦对比图”）：
  - `execute_code` completed，结果字符串是 “Saved figure to: /workspace/output/sin_cos_comparison.png (dpi=150)”；
  - 但计划 artifacts 中没有 `image_base64`，前端拿不到图；
  - `render_plot` 在**新建的 ephemeral 沙箱**中重跑，先缺 numpy、安装 numpy，再缺 matplotlib，最终 `ModuleNotFoundError: matplotlib`，整张计划 failed，耗时约 16 分钟。
- 根因有三处：
  1. 图片采集只认固定路径：`docker-sandbox/internal/engine/docker.go:213-221` 只读 `/workspace/output_plot.png`；`ExecutePython` / `ExecutePythonStream` 也只从 `output_plot.png` 取图（:223-301）。模型保存为其他文件名时图片被静默丢弃。
  2. `render_plot` 没有依赖 `prepared_runtime`：`backend/internal/planner/planner.go:1316-1325` 里它的 required artifacts 只有 `execution_result`（agent planner 版本是 `raw_figure`/`execution_log`），因此 `coder.go:1170-1206` 的 `executeCodeInSandbox` 找不到 runtime，直接 `CreatePersistentSandbox` 新建沙箱。
  3. 缺模块自动安装只允许一次：`coder.go:1238-1268` 用 `moduleRecovered` 单次开关；装完 numpy 再遇到 matplotlib 就 break。

### 为什么修

- 这是最常用的“代码执行 + 图表”路径，也是演示的第一印象；在干净机器上几乎必现失败。
- 失败会额外消耗一次 LLM 代码生成、重复下载 numpy+matplotlib（实测 8 分 36 秒），并留下泄漏容器（见 #6）。
- 修复后不只救一个场景，还能统一所有可视化节点（render_plot / result_visualization / benchmark plot）的产物契约。

### 怎么修

1. **统一产物契约**：
   - 执行器在 `/workspace` 下扫描本次执行新增的 `*.png`（按 mtime 或执行前后快照 diff），支持多图；
   - 或注入 `RA_OUTPUT_DIR=/workspace/output`，prompt 强制 `savefig(os.path.join(os.environ["RA_OUTPUT_DIR"], "result.png"))`；
   - 在事件/artifact 中返回 `image_base64` 或（更推荐）artifact 引用。
2. **render_plot 复用运行时**：
   - `render_plot` / `result_visualization` 的 `RequiredArtifacts` 增加 `prepared_runtime`；
   - `executeCodeInSandbox` 优先使用 `prepared_runtime` / `runtime_session`，只有没有时才新建 ephemeral；
   - 如果上游已经产出图片，render_plot 直接透传/后处理，不重新生成代码。
3. **缺模块恢复循环**：
   - 最多 3 个模块 / 2 轮；每轮把 pip 日志、模块名写入 task metadata；
   - 失败时写入 `MissingRequirement{kind:"dependency_missing"}`，而不是只剩原始 traceback。
4. **提示词与契约对齐**：prompt 中的保存路径、artifact 名称、render_plot 的 required artifacts 三处一致。

**验收**

- 同一个 prompt 连续 3 次执行，计划 completed，`plot_image` 非空且为合法 PNG；
- 日志中 render_plot 不再出现 “Running … in sandbox image”；
- 总耗时 < 5 分钟（有 pip cache / 预装镜像后 < 2 分钟）。

---

## 2. 审批状态机：等待审批会杀死计划，过期审批能“复活”失败计划

### 问题出在哪

- 实测 plan `b93fd2a5-09b2-4882-a9c6-ba923ee7697c`：
  - 08:23:16 进入 `awaiting_approval`；
  - 08:57:51 变成 `failed`，原因 `execution context ended: context deadline exceeded`（`PLAN_MAX_DURATION_SECONDS=2100`，35 分钟）；
  - 之后调用 `POST /runtime-actions/:id/decision {approve}` 返回 200，计划变回 `in_progress`，`install_dependencies` 开始执行，但下游 `execute_code/render_plot/verify_result/general_synthesis` 仍是 `canceled`；
  - 取消计划后，沙箱容器内 `python3 -m pip install numpy matplotlib` 仍在运行（见 #6）。
- 代码根因：
  - `api/plan_runtime.go:244` 用整段 plan timeout 包住执行，审批等待没有独立预算；
  - `api/runtime_actions.go:49-120` 的 `decideRuntimeAction` 只检查 action pending，不检查 plan 是否 failed/canceled；
  - `scheduler.CancelPlan` 只改任务状态，不停止沙箱、不清理 pending action；
  - spec 里的 `approval_timeout` 在代码中不存在（grep 为 0）。

### 为什么修

- 这是“人在回路”审批的安全边界：等待审批不应消耗执行预算，过期审批不应改变已终态计划。
- 当前行为会让用户看到一个“半死”计划：一部分节点 canceled、一部分 in_progress，后续无法重试也无法解释。
- 取消不杀进程会造成真实算力/费用泄漏，并污染后续计划。

### 怎么修

1. 增加独立 `APPROVAL_TIMEOUT_SECONDS`（如 1800s）：
   - 到期把 action 标 `expired`、任务标 blocked、plan outcome=blocked；
   - 写 `MissingRequirement{Kind:"approval_timeout"}`，发 SSE 事件；
   - 不计入 plan 执行时长预算。
2. `decideRuntimeAction` 增加状态守卫：
   - 只允许 `plan.Status == awaiting_approval` 且 action 未过期；
   - 对 failed/canceled 返回 409，并提示“计划已结束，请创建新计划或重跑”；
   - 禁止在 approve 时无条件把 plan 改回 pending/in_progress。
3. 取消计划时：
   - 扫描该 plan 的 pending runtime actions 全部标 canceled；
   - 按 plan label 停止/删除 sandbox；kill 容器内 pip/python 子进程；
   - 写审计事件（谁、何时、为什么取消）。
4. 计划进入终态（completed/failed/canceled）时统一调用 sandbox cleanup（见 #6）。

**验收**

- `APPROVAL_TIMEOUT_SECONDS=5` 测试：等待 6 秒后 plan blocked、missing kind=approval_timeout；
- 对 failed/canceled 计划 approve 过期 action → 409；
- 取消执行中的计划 → 容器内无 pip/python 进程，容器被删除；
- 计划状态不再出现“下游 canceled、上游 in_progress”的混合态。

---

## 3. 身份与 API Token：X-User-Id 可伪造 + 静态 Token 进前端

### 问题出在哪

- `backend/internal/api/cookies.go:33-42` 的 `resolveUserID` 优先信任客户端 `X-User-Id`，只在没有 header 时使用 HttpOnly cookie。
- 实测：
  - 请求头 `X-User-Id: admin-victim` + 共享 Bearer Token，创建的计划 owner 就是 `admin-victim`；
  - 用另一个用户的 `X-User-Id` 可读取其 plan（实测读取 `b93fd2a5` 成功）。
- 前端通过 `VITE_API_AUTH_TOKEN` 把静态 `API_AUTH_TOKEN` 编译进 JS；构建后的多个 `index-*.js` 中都能 grep 到同一 token。

### 为什么修

- “计划只有 owner 能审批/取消/执行”是治理承诺；当前在多用户/共享部署下不成立。
- 静态 token 进浏览器意味着任何能打开前端的人都能直接调用 API，绕过 UI 的权限与审计。
- 这是后续多租户、团队协作、分享链接的前置条件。

### 怎么修

1. **不要在前端 bundle 放长期 token**：
   - 用 BFF/反向代理注入 Authorization；或
   - 登录后由后端签发短时、用户级 session（HttpOnly + Secure + SameSite）。
2. **身份来自服务端**：
   - OIDC/SAML 或至少签名 cookie；
   - 禁止信任裸 `X-User-Id`；如必须保留 header，用 HMAC + 过期时间 + nonce。
3. **统一授权中间件**：
   - plan / upload / document / events / SSE 全部按 principal 过滤；
   - 增加 `GET /api/plans`（owner 过滤）、`GET /api/sessions`；
   - 审计谁在何时创建/批准/执行/取消/下载。
4. **配套限流与配额**：per-user plan 数、上传量、LLM 调用量、并发执行数。

**验收**

- 不带 token → 401；伪造 X-User-Id 读他人计划 → 403；
- 两个用户各自看不到对方计划/上传/SSE；
- 前端静态文件中不再出现 API token；
- 审批/取消/执行事件带真实 principal 的审计记录。

---

## 4. 意图路由：代码性能对比被判成框架评测

### 问题出在哪

- `POST /predict` 对“用 Python 计算并对比 bisect 和 sorted 在 10000 条数据上的查找性能，输出一个柱状图”返回：
  - `intent_type=Framework_Evaluation`、`confidence=0.9`、`entities.frameworks=["bisect","sorted"]`；
- 计划 `0191eeb1-9ace-4be3-981b-9461baedb88f` 生成 13 个节点：framework_research + 两条各 5 节点的“框架基准”分支 + report + render_plot；两条分支各自安装依赖、各自生成代码，最终失败。
- 同一句如果走 rule fallback 会得到 Code_Execution（对比测试中“二分法和摩尔排序”走了 rule_fallback）。

### 为什么修

- 错误意图会生成完全错误的 DAG，消耗大量 LLM/算力，并给用户“系统不懂我”的体验。
- 框架评测模板是 mock/离线基准，无法回答“两个 Python 函数的性能对比”；这是语义级错误，不是样式问题。
- 这是 planner/agent 质量的上游，修好后能减少大量模板回退与重复节点。

### 怎么修

1. **框架白名单**：langchain、llamaindex、haystack、dspy、autogen、crewai、langgraph 等；LLM 返回的 `frameworks` 必须逐个命中，未命中则忽略并按 Code_Execution/General 处理。
2. **修改 intent prompt**：只有“白名单框架名 + benchmark/对比/选型语义”才判 Framework_Evaluation；“比较两个函数/算法/标准库”判 Code_Execution。
3. **postValidate 意图-实体一致性**：不信任 LLM 返回的任意 entities；框架/论文/绘图等关键实体都要校验。
4. **回归集**：bisect/sorted、二分法/摩尔排序、LangChain/LlamaIndex、纯绘图、普通问答；rule + LLM 双模式跑。

**验收**

- 上述 5 类 prompt 的 intent_type 分别符合预期；
- 框架计划只包含白名单框架；非白名单名称不会生成 framework 分支；
- 同一 prompt 在 rule/llm/hybrid 三种模式下不出现 13 节点 mock 计划。

---

## 5. Planner 回退与节点冗余：Agent 规划器大量失败，模板在兜底

### 问题出在哪

- 155 份计划快照：`planner_source=template` 28 份、`agent` 20 份（其余旧数据无 meta）。
- fallback 原因 top：
  - `required artifact has no producer for node <id>: reproduction_mode_report` ×8；
  - `artifact "generated_code" has multiple producers` ×5；
  - LLM EOF / context canceled ×4；
  - framework execute node missing isolated code/runtime artifacts ×3。
- 路由测试中 General 请求出现 3–4 个连续 `general_process` 节点；“hello”只有 1 个节点。
- 计划创建耗时实测 14–50 秒。

### 为什么修

- 用户以为在用“Agent 规划”，实际大量使用模板；模板对非标准任务形状不对，导致错误计划或空转。
- 重复节点增加 LLM 调用、延迟和失败概率；双分支计划的多生产者冲突是可直接修复的契约问题。
- 计划形状不稳定（1/3/5 节点）会影响用户信任与后续自动化。

### 怎么修

1. **Planner 输出后处理**：
   - 合并连续相同类型节点；节点数上限（如 12）；禁止无输出 artifact 的节点；
   - artifact key 按分支命名空间化（`framework_a_generated_code`）或允许按 branch 区分生产者。
2. **确定性 builder 兜底**：每类意图都有确定性 builder（不只 General+paper），agent planner 只是可选优化。
3. **错误回传 self-repair**：把校验错误 JSON 回传给 LLM 做一次修复，再决定是否模板回退；回退原因写进 plan meta 并在 UI 显示。
4. **性能**：intent 分类与 planner 并行/缓存；前端先显示“草稿计划”，再增量补全。

**验收**

- 黄金 prompt 集上 `planner_source=agent` 成功率 ≥ 90%；
- 同一 General prompt 连续 5 次节点数稳定；
- 双分支框架计划不再因 `generated_code` 多生产者回退；
- 计划创建 P95 < 20 秒。

---

## 6. 沙箱生命周期：容器泄漏 + 取消不杀进程

### 问题出在哪

- 4 个终态计划（`b93fd2a5` canceled；`0191eeb1`/`a1678d3c`/`6dd6dd44` failed）仍留下 6 个 running `python:3.11-bullseye` 容器；容器标签正确（`research-assistant.plan-id`），但没有清理逻辑。
- 取消后的 `b93fd2a5` 容器内仍有 `python3 -m pip install numpy matplotlib` 进程。
- 代码只在两处调用 `CleanupSandbox`：`coder.go:800-810`（重建运行时）和 `coder.go:1204-1206`（ephemeral 沙箱）；`prepare_runtime` 创建的 persistent sandbox 从不清理。

### 为什么修

- 每个计划至少一个 persistent 容器；失败/取消的计划会永久占 CPU/内存/磁盘，最终拖垮宿主机。
- 取消/超时后 pip 继续跑，会继续下载、写盘、占用网络，成本不可控。
- 这也是“预算受限自动研究”的基础：不能只限制 token，还要限制运行资源。

### 怎么修

1. **plan 级 sandbox registry**：创建时记录 `plan_id → sandbox_id`；计划终态统一 stop/remove；支持手动清理。
2. **TTL reaper**：例如 2 小时无活动自动清理；清理 /tmp workspace、pip cache 临时目录。
3. **取消/超时 kill**：调用 sandbox `kill/stop`（或 docker kill），确保 pip/python 子进程结束；记录清理事件。
4. **资源看板**：暴露当前 sandbox 数、CPU/内存占用、磁盘用量；健康检查包含 reaper 状态。

**验收**

- 任意计划 completed/failed/canceled 后，按 plan label 查不到 running 容器；
- 取消后 10 秒内 pip/python 进程结束；
- 连续跑 10 个计划，容器数不增长。

---

## 7. 计划存储与 artifact：写放大、重复图片、SSE 全量重放

### 问题出在哪

- `/app/data/plans.json` 约 29 MB / 169,290 行；`plans.json.d` 22 MB / 154 个快照；最大单计划 1.4 MB。
- 同一张 213,884 字符 base64 图在 `final_plot`、`plot_image`、`plot_render_report` 三处重复（plan `70d059df`）。
- `FilePlanStore.UpdatePlan` 每次克隆整个 PlanGraph、写完整快照、fsync、再追加日志；`AppendEvent` 每条事件单独 open/write/fsync。
- `streamPlanEvents` 每次连接/心跳调用 `ListEvents` 克隆全部事件；`task_completed` payload 包含完整 `image_base64`。

### 为什么修

- 计划越大，调度/保存/加载越慢；图片多的计划快照 MB 级；SSE 重连可能传输数 MB。
- 没有 retention/compaction，长期运行会拖垮单机；也导致前端 localStorage 溢出（见 #9）。

### 怎么修

1. **内容寻址 artifact store**：SHA-256 → 文件/对象存储；PlanGraph 只存 `artifact_ref`；图片按 URL 懒加载。
2. **事件模型**：单调 `seq` + `Last-Event-ID` / `after_seq` 分页；SSE 只推增量；task_completed 只带引用。
3. **写入优化**：`UpdatePlan` dirty check；批量/异步 fsync；日志按天滚动 + gzip；plan TTL/归档。
4. **存储迁移**：SQLite/Postgres 分表（plan/node/event/artifact），或至少 plan_summary 索引；启动只加载摘要。

**验收**

- 含 3 张图的完成计划，PlanGraph JSON < 200 KB；
- 重复图片只存一份；
- SSE 重连只传增量（< 100 事件）；
- 100 个计划的启动加载 < 2 秒。

---

## 8. runtime_validate：包名→模块名映射错误

### 问题出在哪

- `backend/internal/agent/coder_runtime.go:154-163` 直接把依赖名中的 `-` 换成 `_` 后 `python3 -c "import <module>"`：
  - `scikit-learn` → `scikit_learn`（应为 `sklearn`）
  - `pyyaml` → `pyyaml`（应为 `yaml`）
  - `Pillow` → `Pillow`（应为 `PIL`）
  - `opencv-python` → `opencv_python`（应为 `cv2`）
- 2026-10-25 DeepWalk 复现报告已记录：runtime_validate 失败但包实际已安装。

### 为什么修

- 这会把“已安装且可导入”的依赖误报为缺失，导致论文复现计划在最后一步失败，浪费前面所有运行时间。
- 这是“环境清单/可复现性”的核心校验，误报会破坏用户对 EnvironmentManifest 的信任。

### 怎么修

1. 用 `importlib.metadata.packages_distributions()` 建立 distribution→module 反向映射；
2. 或读取 `pip show -f <pkg>` / `top_level.txt`，取真实 top-level module；
3. 维护常见包映射表（scikit-learn、pyyaml、Pillow、opencv-python、python-dateutil 等）；
4. 校验失败时同时检查 `pip show <pkg>` 是否成功，区分“未安装”与“import 名不同”。

**验收**

- 依赖 scikit-learn / pyyaml / Pillow 的最小环境校验通过；
- 真正缺失的包仍然报错，并给出 package 与 module 两个名字。

---

## 9. 计划库/会话：无列表 API、游客丢计划、localStorage 溢出

### 问题出在哪

- `GET /api/plans` → 404；后端 `ListPlans` 只用于启动恢复，没有对外列表接口。
- 游客模式前端每次挂载生成新的 `guest-<uuid>` 并作为 `X-User-Id`；刷新后 plan id 丢失，后端留下孤儿计划。
- 4 个大计划 session（每个 1.37 MB，含 base64）写入 localStorage：前 3 个成功，第 4 个 `QuotaExceededError`；代码只 `console.warn`，用户无感知。

### 为什么修

- 研究任务通常几十分钟到几小时；刷新/换设备找不回计划是致命体验。
- 后端已经持久化了计划，但用户看不到、删不掉、导不出；数据只增不减。
- localStorage 存 base64 计划图会很快触顶，且失败静默，属于数据丢失。

### 怎么修

1. 新增 `GET /api/plans?session_id=&cursor=&limit=` 与 `GET /api/sessions`，按 owner 返回摘要（不含 base64）。
2. 游客使用服务端已设置的 `sa_uid` cookie，不要用前端随机 guest id 覆盖；刷新后可恢复最近计划。
3. 前端只存轻量索引（plan id/标题/时间/状态）；大对象放 IndexedDB 或后端。
4. 配额失败时给用户明确提示，并降级为服务端恢复；增加删除/归档/搜索。

**验收**

- 刷新页面后仍能看到并打开刚运行的计划；
- 4 个大计划不导致 quota 崩溃；
- 删除计划后列表/详情都不再返回；
- 跨浏览器登录可看到同一用户计划。

---

## 10. 冷启动与依赖安装：镜像预检、缓存、超时

### 问题出在哪

- 第一次画图：`prepare_runtime` 返回 500，`Docker run failed: Unable to find image 'python:3.11-bullseye' locally`；代码执行链没有 `image_prepare` 节点，而论文链有。
- numpy+matplotlib 安装 08:23:21 → 08:31:56 = 8 分 36 秒，pip 下载约 80–100 KB/s；`render_plot` 又重装一遍。
- 默认任务超时 600 秒（`planner.go:164`），重依赖（torch 等）必然接近/超过；2026-10-25 DeepWalk 报告已记录 10 分钟超时失败。

### 为什么修

- 冷启动失败是“新用户第一次使用”的致命问题；依赖安装时间是当前最大的单任务延迟来源之一。
- 同一依赖在每个计划/每个沙箱重复下载，浪费带宽与时间，也让“最小复现”不可预测。
- 镜像 digest 不预检会让 EnvironmentManifest 与实际运行环境不一致，影响可复现性。

### 怎么修

1. **所有执行链加 image_prepare**，或让 `prepare_runtime` 内部调用 `EnsureImage`（allowlist + pull + digest）；失败写 `MissingRequirement{kind:"image_pull_failed"}`。
2. **镜像源配置**：`SANDBOX_REGISTRY_MIRROR` / 前缀重写；支持内部 registry；UI 显示镜像准备状态与大小。
3. **依赖缓存**：预装科学栈镜像（numpy/pandas/scipy/matplotlib/scikit-learn）；共享 pip cache volume 或 uv/wheelhouse。
4. **超时策略**：按 `RuntimeSpec.TimeoutSec` 或估算下载量动态设置（max(600, size/100KB/s×3)）；pip `--retries`/`--timeout`/hash/lock。

**验收**

- 全新机器第一次画图 < 5 分钟成功；
- 第二次同环境 < 2 分钟（缓存命中）；
- torch 级别依赖有明确预计时间与可配置超时；
- EnvironmentManifest 的 image digest 与实际运行镜像一致。

# 剩余未修问题清单

> 本文覆盖**除“最值得先修的 10 个点”之外**、本次审计确认但尚未修复的问题。
> 这些问题不一定都紧急，但都值得进入 backlog；其中安全、数据丢失、可复现性和可观测性优先。
> 说明：本次审计未修改项目源码；以下均为建议，不代表已实现。

## 速览

| 编号 | 领域 | 问题 | 优先级 | 影响 |
|---|---|---|---|---|
| R-01 | 安全/资源 | Sandbox 默认 bridge 可访问公网；network gate 未生效；硬限制只有定义 | P1/P2 | 数据外泄、资源超限 |
| R-02 | 前端 | runtime action 待批时错误显示“审批并运行”；状态文案/命名/假刊头 | P2 | 审批误操作、信任下降 |
| R-03 | 前端 | 自动执行与聊天文案矛盾；消息顺序反向 | P2 | 用户不知道计划是否已开始 |
| R-04 | API | 300k 字符 intent 可提交；无 rate limit/幂等；health 泄露 provider | P2 | 成本攻击、重复计划 |
| R-05 | 可观测性 | 无 metrics/OTel/token-cost；无计划看门狗 | P2 | 排障靠 grep，成本不可见 |
| R-06 | 检索 | README 的 BM25 实为 TF/长度打分；Librarian 一次性拼接；无 OCR | P2 | 检索质量与文档不符 |
| R-07 | 文献能力 | 无 arXiv/S2/OpenAlex/Crossref/GitHub 检索与 DOI 导入 | P2 | 研究助理缺少“真实检索” |
| R-08 | 数据生命周期 | 无 plan/upload 删除、归档、导出；上传 68 MB；无 hash 去重 | P2 | 数据只增不减、无法交付 |
| R-09 | 可靠性 | SSE ticket 60s 过期后重连失败；无 Last-Event-ID；onerror 关闭流 | P2 | 长任务断线后无法恢复 |
| R-10 | 调度/性能 | 150–200ms 轮询；每 tick UpdatePlan；maxConcurrent 硬编码 2；无全局队列 | P2 | CPU/IO 浪费、无公平性 |
| R-11 | 测试/CI | 覆盖率 58.1%；CI 无真实后端 E2E；无故障注入/负载/安全测试 | P2 | 回归靠人肉 |
| R-12 | 代码质量/文档 | 巨型 Go 文件；string context key；emoji 错误文案；compose 乱码；README 与实现不符 | P2 | 维护成本、体验不一致 |
| R-13 | 产品缺口 | 无计划编辑/暂停/恢复、通知/调度、协作、CLI/SDK/OpenAPI、多租户 | P2 | 从原型到平台的距离 |
| R-14 | 错误处理/UX | 通用错误文案、原始 traceback、missing requirements 未展示、长日志无虚拟化 | P2 | 用户无法自助排障 |
| R-15 | 安全加固 | sandbox 以 root 运行、可写 rootfs、Docker socket 高权限、无 gVisor | P2 | 容器逃逸/供应链风险 |
| R-16 | 元数据 | Attention 作者仅 4/8、year=2023；Louvain 作者被拆成 “Renaud”/“Lambiotte” | P2 | 论文元数据不可信 |
| R-17 | 上传安全 | 无病毒扫描；PDF 解析与后端同容器；无 SHA 去重 | P2 | 恶意文件/资源浪费 |

---

## R-01 Sandbox 网络与硬限制：安全策略名存实亡

### 问题出在哪

- 容器默认 `--network bridge`（`docker-sandbox/internal/engine/docker_security.go:29`）；沙箱内可解析 pypi.org，拥有完整公网 egress；
- `runtimeActionForTask` 只在 `paper_code_execute` 且 `spec.NetworkAccess=true` 时请求网络审批；而 `reproduction_spec.go:84` 把 `NetworkAccess` 硬编码为 false，所以该 gate 实际不会触发；
- `MaxImageBytes / MaxDownloadBytes / MaxDiskGB / MaxRuntimeSec` 只出现在结构体定义和默认值，没有任何执行代码读取（grep 仅 8 处定义）。

### 为什么修

- “确认模式”承诺镜像/依赖/数据/网络前暂停；当前沙箱始终有网络，承诺不成立。
- 任意仓库代码可以在沙箱内发起外联、下载未审批内容、把上传论文/数据外传；结合 Top10 #3 的 token 问题，风险更高。
- 没有硬限制，单个任务可以占满磁盘/网络/运行时长。

### 怎么修

1. 默认 `--network none` 执行代码；安装依赖/下载数据时按任务切换到 allowlist 代理（pypi、npm、github、arxiv）。
2. 在 sandbox 创建前按 `RuntimePolicy` 检查镜像大小、磁盘、运行时长；运行时用 cgroup / storage-opt / timeout 强制。
3. 记录实际网络流量、下载 URL/字节数，写入 `EnvironmentManifest` 与审计事件。
4. 把 `network_access` gate 与沙箱实际网络模式绑定，而不是只看 spec 字段。

**验收**

- 默认代码执行任务无法解析公网域名；
- 需要联网的任务必须出现 runtime action，并在批准后才可出网；
- 超限任务被拒绝/终止，事件里有原因与用量。

---

## R-02 前端审批按钮与状态文案

### 问题出在哪

- runtime action pending 时，图版右上角显示“审批并运行”（计划级审批），点击调用 `/approve` 返回 409；正确操作是下方 runtime action 卡的“允许一次/拒绝”。
- 根因：`useGraphExecutionViewModel.ts:51,72` 只判断 `activePlanStatus==='awaiting_approval'`，没有判断 `approval.required`。
- 表 1 只映射 completed/in_progress/failed/awaiting_approval；`blocked`/`canceled` 显示“待校”；`answer_agent` 显示 `ANSWER_AGENT`；依赖安装卡标题固定“安装论文依赖”；刊头“第 12 卷 · 第 4 期 · 2026”是硬编码假元数据。

### 为什么修

- 审批是高风险操作；错误按钮会让用户以为点“审批并运行”可以继续，实际 409，破坏信任。
- 状态文案错误会让用户误判节点状态（blocked 被当作待处理），影响重试/取消决策。
- 假刊头和数据无关，显得不专业。

### 怎么修

1. `requiresApproval` 拆成“计划审批”和“运行时审批”两个状态；计划审批才显示“审批并运行”，运行时审批只显示“允许一次/拒绝”。
2. 补全 `blocked`/`canceled`/`skipped` 的状态文案与颜色；`answer_agent` 映射为“回答智能体”。
3. 运行时标题按上下文生成（“安装运行依赖”而不是“安装论文依赖”）。
4. 刊头改为真实计划数/日期/版本，或移除卷期。
5. 加 Playwright 断言：runtime action pending 时不存在“审批并运行”按钮；点击允许后计划继续。

**验收**

- 两种审批状态 UI 明确区分，无 409 误操作；
- blocked/canceled 节点显示正确；
- 刊头数据来自真实 plan/date。

---

## R-03 自动执行与聊天文案矛盾

### 问题出在哪

- `ResearchAssistantApp.tsx:371-376` 对 Code_Execution + render_plot 计划在创建后自动 `runAllTasks`；
- 但聊天仍提示“核对节点与依赖后，在右侧执行台启动计划”；
- 实际消息顺序是“已启动整图执行”先于“执行图已生成”。

### 为什么修

- 用户不知道计划已经自动开始，可能以为还在草稿阶段；与“确认模式”的预期冲突。
- 消息顺序反向会让对话记录不可信，影响审计与复盘。

### 怎么修

1. 产品决策：要么所有计划都需手动“运行计划”，要么在创建前提供“自动运行”开关；不要只对 plot 计划隐式自动运行。
2. 如果保留自动运行，消息文案改为“已生成执行图并自动启动；可在执行台取消”，且先发“执行图已生成”再发“已启动”。
3. 在计划详情显示触发方式（手动/自动）。

**验收**

- 同一计划的消息顺序固定且与真实执行状态一致；
- 用户能在创建前选择是否自动运行。

---

## R-04 API 健壮性：长度、限流、幂等、健康信息

### 问题出在哪

- 300,000 字符的 intent 返回 200（intent service 本身有 20k 限制，但 backend 不校验）；
- 3 次快速 plan 创建全部接受（8s/27s/42s），无 rate limit；
- 无幂等键；双击/网络重试会重复创建计划；
- `/api/health` 未鉴权，返回 provider `base_url` 与 model。

### 为什么修

- 超长 prompt 会放大 LLM 成本/延迟，甚至触发 provider 限制；
- 无 rate limit/配额，单个用户可耗尽共享 LLM 配额；
- 重复计划会重复执行/计费；
- health 泄露内部 provider 信息，便于攻击者探测。

### 怎么修

1. backend 校验 intent 长度 ≤ 20k、附件数/大小限制、plan 并发 per-user 上限。
2. per-user token bucket + 队列；plan 创建返回 429 并可重试。
3. 支持 `Idempotency-Key`，服务端缓存 24h 的创建结果。
4. health 返回 ok/version/依赖状态；provider 详情放内部端点或日志。

**验收**

- 300k intent → 400；超并发 → 429；
- 相同 Idempotency-Key 只创建一个计划；
- 未鉴权 health 不含 provider URL/model。

---

## R-05 可观测性与成本账本

### 问题出在哪

- 无 `/metrics`、无 OpenTelemetry 导出；
- 计划只有 attempts/duration 预算，没有 token/cost 记录（grep 无 token usage 模型）；
- 只有 `log.Printf` 与 SSE，排障靠 grep；无计划级 trace 查看。

### 为什么修

- 研究任务耗时长、调用多；没有 token/费用/延迟指标，无法做预算与优化。
- 无法回答“这次复现花了多少钱/为什么慢/哪个节点失败率最高”。
- 生产化需要 SLO、告警、排障链路。

### 怎么修

1. 记录每节点 LLM usage（prompt/completion tokens、cost、latency）、sandbox duration、task attempts。
2. Prometheus 指标：plan_duration、task_failure_rate、llm_latency、llm_tokens、sandbox_duration、active_plans、event_backlog。
3. OTel trace per plan/task，事件带 trace_id/span_id；Grafana 看板。
4. 计划看门狗：长时间无心跳/无事件告警；sandbox reaper 指标。

**验收**

- 计划详情能看到 token/费用/各节点耗时；
- Grafana 有 5 个核心指标；
- 故障计划可通过 trace 定位到节点与 LLM 调用。

---

## R-06 检索质量与文档智能

### 问题出在哪

- README 称 DocumentStore 支持 BM25，但 `documents/store.go:224-236` 的 `scoreChunk` 是词频/长度归一化，没有 IDF、k1、b；
- Librarian（`librarian.go:105-143`）一次性拼接首页 + top 10 chunks + 固定英文关键词，最多 24k 字符，没有迭代检索/章节遍历；
- PDF 仅 `pdftotext`，无 OCR；扫描版 PDF 无文本层时无法分析。

### 为什么修

- 检索质量直接决定论文分析/复现的准确性；当前“BM25”名不副实，长文档容易漏掉关键章节。
- 一次性拼接无法处理 50+ 页论文；模型看不到方法/实验细节，容易编造。
- 扫描版论文在科研场景很常见，直接失败会损失用户。

### 怎么修

1. 实现真正 BM25（IDF、k1、b、长度归一化）或 hybrid（BM25 + embedding + rerank）；提供 `top_k`、页码、section。
2. Librarian 改为 agentic retrieval：先目录/章节树，按需读页/读节，最后做引用校验。
3. 接入 OCR（Tesseract/PaddleOCR），版面/表格/图注抽取；解析失败给出明确 missing requirement。
4. 引用校验：LLM 引用必须能定位到原文 span，否则标红。

**验收**

- 50 页论文检索能命中方法/实验章节；
- 引用 100% 可在原文定位；
- 扫描版 PDF 能提取文本并完成分析。

---

## R-07 真实文献/网页检索与导入

### 问题出在哪

- task registry 明确写 `general_research` “no dedicated web-search tool”；
- 框架调研也是 LLM 知识，没有 arXiv/Semantic Scholar/OpenAlex/Crossref/GitHub token 集成；
- 用户必须上传 PDF，不能粘贴 arXiv/DOI URL 自动导入。

### 为什么修

- “Research Assistant”如果没有真实检索，就只是“LLM + 上传文件”，无法完成文献综述/选型/复现调研。
- 当前 paper_metadata 只有 PDF 内文本；无法验证论文是否真实存在、是否有官方仓库。

### 怎么修

1. 接入 arXiv / Semantic Scholar / OpenAlex / Crossref / PubMed / GitHub Search，带缓存、限流、token 配置。
2. 支持 URL → 下载 PDF + 元数据 + 官方仓库候选。
3. 引用图谱、论文库、去重（DOI/arXiv ID）。
4. 检索结果写入 ResearchContext，节点通过 InputBindings 消费。

**验收**

- 输入 arXiv URL 可直接生成带元数据/PDF 的计划；
- 文献调研结果带可点击来源；
- 重复 DOI 不重复下载。

---

## R-08 数据生命周期与导出

### 问题出在哪

- 所有路由中无 DELETE；计划/上传/会话无法删除或归档；
- `/app/uploads` 已 68 MB；无 hash 去重、无配额、无清理；
- 没有 artifact 下载/报告导出接口；图片只在前端 base64 显示。

### 为什么修

- 研究数据只增不减，最终撑爆磁盘；用户无法管理自己的数据。
- 无法把结果带走（PDF/DOCX/Markdown/zip），不符合科研交付习惯。
- 重复上传同一 PDF 会重复占用空间与解析时间。

### 怎么修

1. 增加 DELETE/archive：plan、session、upload；软删除 + TTL 清理。
2. 上传按 SHA-256 去重；per-user 配额；清理孤儿文件。
3. artifact 下载接口（图片/代码/指标/日志/环境清单）；计划导出 zip/Markdown/PDF。
4. 上传病毒扫描与 PDF 解析隔离（见 R-17）。

**验收**

- 删除后列表/详情/文件都不可访问；
- 同一 PDF 二次上传不重复存储；
- 一键导出可重建的最小复现包。

---

## R-09 SSE/HTTP 可靠性

### 问题出在哪

- SSE ticket TTL 60 秒；EventSource 自动重连会复用旧 URL，过期后 401/403；前端 `onerror` 直接 close，不重试；
- 事件没有单调 seq/Last-Event-ID；每次重连都从头 replay，长计划会很慢；
- 计划创建 14–50 秒，而 axios 默认 60 秒；超时后后端可能已创建计划，前端只看到错误；
- 无幂等键，重试会重复创建（见 R-04）。

### 为什么修

- 长任务（几十分钟）网络抖动很常见；断线后不能恢复会丢失实时进度。
- 全量 replay 会让重连更慢，形成恶性循环。
- 创建超时会造成“孤儿计划”，用户既看不到也删不掉。

### 怎么修

1. SSE 事件加 `seq`；支持 `Last-Event-ID`/`after_seq`；重连时刷新 ticket（或 ticket 绑定 session 而非一次性）。
2. 前端 onerror 做指数退避 + 重新获取 ticket + 从最后 seq 恢复；不要直接 close。
3. 计划创建改为异步 job + 轮询/SSE 进度，或延长 axios timeout 并加 Idempotency-Key。
4. 后端在客户端断开后仍继续执行（已有），但 UI 要能按 plan id 恢复。

**验收**

- 断网 10 秒后自动恢复，不重复日志；
- 计划创建超时后可通过列表/ID 找回；
- SSE 重连只收增量。

---

## R-10 调度与并发性能

### 问题出在哪

- `scheduler.ExecutePlan` 以 150–200ms 轮询；每轮都 `UpdatePlan`（即使无变化），导致快照写放大；
- `NewScheduler(..., 2)` 的 maxConcurrent 硬编码 2，不能按计划/用户配置；
- 无全局队列/公平性；多个计划可同时启动，资源竞争；
- 大文件（planner.go 85KB、autoresearch.go 80KB、coder.go 71KB）难以维护。

### 为什么修

- 轮询 + 全量写会浪费 CPU/磁盘，计划越多越明显；
- 没有全局并发控制，单用户可占满 LLM/容器；
- 巨文件导致修改风险高、review 困难。

### 怎么修

1. 调度改为事件驱动（channel/condition variable），只在状态变化时写；至少 dirty check。
2. maxConcurrent 配置化（env/per-plan），增加全局 worker pool + 优先级 + per-user 配额。
3. 拆分 planner/agent 巨型文件为子包（planbuilders、agents/coder/runtime、agents/autoresearch/...）。
4. 增加调度延迟/队列长度指标。

**验收**

- 空闲计划 CPU 接近 0；等待审批不再写快照；
- 10 个并发的计划按配额公平调度；
- 单文件 < 30KB，包边界清晰。

---

## R-11 测试与 CI 缺口

### 问题出在哪

- 覆盖率 58.1%；关键路径（工具、events、uploads、models）无测试；
- CI 只跑 a11y/visual，不跑真实 backend E2E；
- 无故障注入（LLM EOF、pip 超时、沙箱创建失败、审批拒绝、后端重启）；
- 无负载/安全回归；visual baseline 仅 Linux。

### 为什么修

- 本次发现的绘图/审批/路由问题都没有回归用例；修完可能再坏。
- 真实 E2E 是唯一能发现“计划形状 + 沙箱 + 依赖”组合问题的手段。
- 安全/负载问题上线后才暴露，成本更高。

### 怎么修

1. 建“黄金场景”E2E：5 类 intent + 绘图 + 论文分析 + 审批取消 + 失败重试；docker compose + mock LLM 或真实 LLM。
2. 故障注入矩阵：LLM 超时/EOF、pip 超时、sandbox 失败、审批拒绝、后端重启。
3. 安全测试：X-User-Id 伪造、超长 intent、路径遍历、SSRF、上传伪造、rate limit。
4. 覆盖率门槛分模块提升（api/scheduler/agent ≥ 70%），关键函数 mutation test。
5. 前端 a11y/visual 增加审批卡、长日志、移动端图交互。

**验收**

- 每个 P0 缺陷都有回归用例；
- CI 阻断计划形状/审批状态/沙箱清理回归；
- 负载下 P95 达标。

---

## R-12 代码质量与文档一致性

### 问题出在哪

- 巨型文件、string context key（`ctx.Value("logChannel")`）、emoji 错误文案（“哎呀，服务器好像开小差了…”）；
- docker-compose.yml 中中文注释出现乱码（GBK/UTF-8 混用）；
- README 称“BM25”“make package 单文件服务”等，与实现/现状不一致；
- `ask_user_question` 后端工具未接入 UI；`tool/`、`docker-core/` 与产品关系不清。

### 为什么修

- 维护者需要花时间确认“文档说的和代码做的是否一致”；
- string context key 容易冲突；emoji 文案与严肃科研产品调性不符；
- 死代码/实验模块增加认知负担。

### 怎么修

1. 统一 typed context key；把 prompts/错误文案集中到 i18n 资源。
2. 修复 compose 注释编码；CI 加 UTF-8 检查。
3. 更新 README/模块 README，标注实验模块与产品模块边界；删除或归档死代码。
4. 如果保留 `ask_user_question`，接入 UI 并加 E2E。

**验收**

- 文档与实现一致；无乱码；
- context key 有类型；错误文案统一；
- 死代码有明确归属或删除。

---

## R-13 产品能力缺口（Top10 之外）

### 问题出在哪

- 无计划编辑/暂停/恢复/从某节点运行；
- 无通知/定时/watch list；
- 无协作/分享/角色；
- 无 CLI/SDK/OpenAPI；
- 多租户只做了“假身份”（Top10 #3 只解决基础认证，不包含 RBAC/团队/项目）。

### 为什么修

- 研究任务是长周期、迭代式的；不能编辑/暂停会迫使用户重跑整图。
- 用户不可能一直盯屏；通知是基本体验。
- 团队协作和 API 化是从“个人工具”走向“平台”的关键。

### 怎么修

- 按《新增功能建议》分阶段实现：先 Plan Library/导出/成本，再 HITL/通知/CLI，最后协作/多租户。

**验收**

- 见新增功能文档的验收标准。

---

## R-14 错误处理与 UX 细节

### 问题出在哪

- 后端错误多为英文原始错误/长 traceback；前端统一显示“服务器开小差”；
- missing_requirements 在部分路径没有结构化返回；
- 长日志一次性渲染，无虚拟化/折叠；
- 移动端图交互有限（缩放/拖拽/详情）。

### 为什么修

- 用户无法自助排障，只能截图求助；
- 长日志会卡顿；移动端无法完成基本操作。

### 怎么修

1. 统一错误码/人类可读文案/修复建议；前端展示 missing_requirements + remediation。
2. 日志虚拟列表 + 级别/来源过滤 + 下载。
3. 移动端图支持缩放/拖拽/节点详情抽屉。

**验收**

- 失败计划能看到“缺什么/怎么修”；
- 10k 行日志不卡顿；
- 390px 视口可完成审批/查看节点。

---

## R-15 安全加固（Top10 #3 之外）

### 问题出在哪

- sandbox 容器默认 root、可写 rootfs（`SANDBOX_READ_ONLY_ROOT=false`），未启用 gVisor/Kata；
- executor 持有 Docker socket（高权限边界）；
- 上传无病毒扫描；PDF 解析与后端同容器；
- 无审计日志导出。

### 为什么修

- 不可信仓库代码/PDF 是主要攻击面；容器逃逸或供应链攻击会影响宿主；
- 科研数据可能敏感，需要审计与隔离。

### 怎么修

1. 默认非 root + read-only root + tmpfs workspace；可选 gVisor/Kata。
2. 上传病毒扫描；PDF 解析放独立沙箱；限制解析资源。
3. 审计日志（谁/何时/对什么/结果）可导出；密钥管理。
4. 定期依赖漏洞扫描（govulncheck/npm audit）。

**验收**

- 安全基线扫描通过；
- 恶意 PDF/仓库样本无法逃逸或拖垮宿主；
- 审计日志可检索/导出。

---

## R-16 元数据提取质量

### 问题出在哪

- Attention PDF：标题正确，但作者只抽到 4/8（Ashish/Noam/Niki/Jakob），year=2023（应为 2017）；
- Louvain PDF：作者被拆成 “Renaud” 和 “Lambiotte” 两个条目；
- 没有置信度/来源提示，用户无法知道元数据是否可信。

### 为什么修

- 元数据是引用、检索、复现的入口；错误会传播到报告与 Claim Evidence。
- 用户需要知道“这是从 PDF 抽的/从 arXiv 查的/LLM 猜的”。

### 怎么修

1. 用 arXiv/Crossref 元数据校正 PDF 抽取结果；作者按分隔符/机构上标合并。
2. 输出 confidence 与 source（pdf/arxiv/crossref/llm）；低置信度高亮。
3. 加单元测试：Attention/Louvain/DeepWalk 三份 fixture 的作者/年份断言。

**验收**

- 三份 fixture 的作者数/年份正确；
- UI 显示来源与置信度。

---

## R-17 上传安全与去重

### 问题出在哪

- 只做扩展名 + 512 字节 content-type 检查；无病毒扫描；
- 同一 PDF 多次上传会重复存储/解析；
- PDF 解析（pdftotext）与后端同容器，恶意 PDF 可能利用解析器漏洞。

### 为什么修

- 上传是外部输入入口；研究场景会收到各种 PDF/代码/数据。
- 重复上传浪费磁盘与解析时间。

### 怎么修

1. SHA-256 去重；per-user 配额；孤儿清理。
2. 病毒扫描（ClamAV）或至少 sandbox 解析。
3. 解析器独立进程/容器，限制 CPU/内存/时间；失败可见。

**验收**

- 同文件二次上传秒回同一 document id；
- 恶意样本被拦截/隔离；
- 解析超时有明确错误。

---

## 建议处理顺序

1. **先做 R-01 / R-09 / R-11**：它们与 Top10 的 #2/#6/#7 相互放大，属于同一批稳定性工作。
2. **再做 R-05 / R-10 / R-12**：可观测性 + 调度 + 代码质量，降低后续修复风险。
3. **随后 R-06 / R-07 / R-08 / R-13 / R-17**：检索、数据生命周期、产品能力。
4. **R-02/R-03/R-04/R-14/R-16** 可穿插做，多数是 1–3 人日的小修。
5. **R-15/R-17** 在多用户/生产部署前必须完成。

> 备注：本文与《最值得先修的 10 个点》共同覆盖本次审计发现；新增功能见《新增功能建议》。

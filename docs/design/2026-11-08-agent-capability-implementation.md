# Agent 能力改进：六线实施与验收

日期：2026-11-08；分支：`feature/agent`，基于已有 `feature/context` 实现。

本轮按 [Agent 能力改进方案](2026-11-07-agent-capability-improvement.md) 推进 AG-01–AG-06，保留现有专业 Agent、Scheduler、ToolInvoker、计划 revision 和上下文存储。设计文档中的旧代码描述不作为实施前提；例如固定 1024 输出、修复快照只存 hash，已在上下文分支解决，本轮补齐调用链和恢复边界。

六条工作线均有代码与确定性回归交付，但不表示原设计全部验收场景已经过真实模型和科研任务验证。下文区分运行机制、离线证据和未交付范围。专项说明：

- [AG-02/06：分类恢复与修复记忆](2026-11-08-recovery-memory.md)
- [AG-03：Claim Evidence 数值验收](2026-11-08-claim-verification.md)
- [AG-04：上下文集成与恢复](2026-11-08-agent-context-implementation.md)
- [AG-05：任务质量基线](../test/2026-11-08-agent-quality-baseline.md)
- [上下文底座首批实现](2026-11-07-context-implementation.md)

## 1. 交付概览

| 工作线 | 本轮运行行为 | 实际边界 |
|---|---|---|
| AG-01 | Profile 可显式选择 `tool_loop`，执行模型原生工具请求、回传结果、继续生成；模型步和工具数受限，检查输出与冻结契约，持久化确认边界 | 默认仍为 `single`；只给确认支持原生工具的模型启用；未知副作用阻塞，不保证所有外部系统可自动精确续跑 |
| AG-02 | typed 失败分类；HTTP、局部修复与调度重跑共享恢复链；持久化计数、退避和无进展阻断；产生一次受审 replan 提案 | 不自动修改运行中 DAG，不自动扩大权限或放宽科研目标；真实复杂环境恢复率尚未测量 |
| AG-03 | 冻结数值合同，校验执行与原件来源；从原始预测和冻结标签重算指标，再进行确定性比较 | 仅有程序自报 JSON 指标时保留待审，不升为独立验证；不自动把任意 stdout 转成可信观测 |
| AG-04 | Profile 接收统一计划/输入/记忆/契约；专业修复调用进入实际模型预算与上下文持久化；revision/阶段隔离和失败快照写回 | 视觉独立调用和材料选取仍有各自限制；不宣称服务商原生压缩能力已普遍可用 |
| AG-05 | 区分 template/model/task，缺执行或 grader 不通过；task 使用生产 ProfileRunner 与 ToolInvoker；冻结参考不进入模型 | 本轮是确定性模型替身与工具执行基线，真实模型成功率、科研质量和成本收益为 N/A |
| AG-06 | 只从实际、有前后证据的已验证修复生成 procedure 候选；环境/协议兼容过滤；独立案例计数与本次错误来源修正 | 默认候选待审，单例仍是 inferred；不会凭记忆自动执行命令；完整复用生命周期与净收益需后续任务证据 |

## 2. Profile 执行与恢复边界（AG-01）

主要入口为 `internal/agentprofile/runner.go`、`loop.go`、`run_store.go`，复用 `internal/tools/Invoker`；协议扩展位于 `oaichat`、`responses`、`modelresolver`。模型返回的文本命令不会被当作工具请求执行。

循环保存完整模型响应后才派发其中的工具请求；同一响应的多个工具逐个检查和计数。有效工具集合同时受 Profile allowlist、节点契约、owner 归属、grant 和工具版本约束。显式空集合保持为空，不能在克隆或 JSON 恢复时扩大权限。参数继续经过既有 schema、超时和审计路径。

审批绑定服务端 owner/plan/task、稳定本地 call ID、工具版本、参数 hash 和有效期。模型文字和任务 metadata 中的批准字符串没有授权效力。等待审批时保存 pending call；批准后续接该动作。工具结果以资料身份回传，原始 Artifact 与裁剪后的模型视图分开保存，截断会显式标记。最终 JSON 派生的 Artifact 标为 `model_output`，不冒充实际执行证据。

每个逻辑模型步重新解析实际模型，并保存 profile ID/revision、model、协议和输出预算；该步预检、压缩和生成固定同一配置。步数在派发前提交，重复请求/重启不清空已有步数。输出非空、生成完成状态、Profile schema、节点 schema 与必需产物分别检查；格式修复同时消耗剩余模型步和共享恢复额度。科学达标与否另由验收器决定。

检查点按 owner/project/session/plan/task/revision 隔离；绑定 Profile、Prompt、契约和初始材料。配置或材料变化不能直接接到旧 run，需新 revision。确认的工具结果可从检查点恢复，不重放；若进程在外部派发后、结果提交前中断，恢复为 `outcome_unknown` 并阻塞，先核查外部状态。中断的模型生成可在保留已有步数计数的前提下重新请求，但不由不完整流式参数派发工具。每次提交和派发前继续检查取消、execution lease/epoch 和 guard。

检查点是本地单 writer 的私有文件存储，以锁和原子替换保护；没有多主分布式存储保证。它补齐 Profile 的动作边界，不把所有专业实验执行器改成可任意自动重放的事务系统。

## 3. 分类恢复与记忆（AG-02/06）

Scheduler 为同一个逻辑任务保留恢复链。`RecoveryState`、`FailureInfo`、`RecoveryDecision` 和动作账本随 fenced 执行结果持久化；恢复动作预留失败时不会继续派发。缺省最多 2 个额外恢复动作、64 次实际模型 HTTP 尝试，原始请求也计入 HTTP 总数。嵌套 HTTP 重试、专业修复与外层重跑共享额度；手动重跑不隐式赠送新的恢复额度。

默认分类恢复只对瞬时错误自动退避重试；429/503 等参考 Retry-After。缺输入、权限、取消和预算不足分别阻塞或停止。确定性失败在相关输入/环境状态未变时不能重复恢复。已接入 Coder 自修、依赖/Python 修复、Benchmark 输出/adapter 和 Paper Debug 修复；AutoResearch 的正常候选搜索不伪装成错误恢复。独立嵌入式调用若未提供共享 controller，继续兼容既有调用行为，不能据此宣称其已有持久化跨层总预算。

环境或确定性问题可产生最多一次受审 replan 提案，包含失败证据、建议变化和冻结验收边界；本轮不自动切换 live DAG。已有 revision 控制路径仍负责授权、并发和结果失效；“在途取消、选择性复用、CAS 原子切图”的全自动恢复流程不在本次完整交付声明中。未达科研目标但证据完整的任务是正常负结果，不进入修复循环。

记忆仅从具有实际 patch、不同前后状态 hash、执行 ID、环境/协议以及前后验证证据的 repair ledger 生成候选。只有重试成功时仍记 episode，不生成安装依赖或修改代码的虚构 procedure。单独执行一次只能支持 `single_case`，trust 为 `inferred`，默认 `pending`；显式启用 promotion 也不改变其证据等级。检索检查 owner、scope 和环境/协议兼容性，不能因候选命中而自动取得权限。

生产调度记录实际 retrieved；任务输入 `adopted_procedure_ids` 可显式选择本轮检索到、仍为 active 且归属/环境/协议兼容的候选，记录 adopted。实际 Coder repair ledger 绑定这些 ID 与前后原件，结果边界据账本记录 executed/verified，并按执行 ID 与阶段去重；失败验证可降回待审。没有实际动作证据时保留 unknown，不从自由文本猜测模型是否采用了记忆，也不宣称所有 Agent 自动关联复用阶段。AutoResearch 的独立案例数按唯一成功 evaluation execution ID 计算，不再使用 trial/search 总数；任务记忆优先采用本次 `result.Failure/result.Error`。

2026-11-09 提交前修复 procedure 使用记录的并发更新：追加 uses、按执行/阶段去重及失败降级在同一次存储事务中完成，避免并行检索/采用覆盖彼此记录或将失败历史与降级状态分开提交。并发回归验证重开存储后记录无丢失、无重复，兼容属性保留且失败候选保持待审；memory 包 race 通过。

## 4. 数值验收与任务质量（AG-03/05）

数值 criterion 的模型建议、后端观测和确定性 verdict 分开存储。合同在测量前冻结，包含预期值、比较方向、容差、单位、seed/聚合以及 dataset/split、协议、代码、环境和原件来源。后端验证真实 DAG 上游、完成 execution ID/epoch/revision、冻结时间与原件 hash，不能由任务输入自授可信身份。

`benchmark_predictions` 从冻结 dataset JSONL 读取标签，要求每个 seed 完整覆盖 split，无重复或未知 index，复用 Benchmark 的 accuracy、macro F1、MSE/MAE 计算器独立重算。百分数/比例和时间单位按冻结规则换算；比较处理绝对/相对/组合容差、方向、精确边界与零参考值，不接受 NaN/Infinity、非法范围或缺 seed。仅这类完整可重算证据支持最终 verified/contradicted。

`json_metric` 可以解析并比较具有来源的执行 JSON，但标记 `review_required=true`，最终仍 unverifiable/needs-review；旧 stdout、模型 `observed_value` 和自报 verified 不会升级为可信数值。真实负结果仍 completed；某项数值达标也不能覆盖其他 criterion 缺证据。

评测必须显式提供用例和 grader。model 模式缺 completer、nil/空响应、未完成响应、被复用的回答或工具请求均不拿模板代替模型输出。task 模式通过生产运行器执行授权工具，独立验证 JSON Pointer 参考、输出 schema、真实工具调用、参数/结果 hash、同一调用绑定的 Artifact、停止原因与预算。参考答案不会发送给模型，也不写入研究记忆。eval session/run 单独隔离，不修改生产任务状态。

每个用例分为 passed/failed/error/skipped，同时报告计划、尝试、评分和错误分母。零可评分用例为 N/A；旧兼容 `score` 字段不能单独解释为成功率。冻结集 `task-quality/v1` 共 8 例：2 个正确正例和 6 个假成功反例，判分器 8/8 符合预期、假通过 0/6。这是判分器验收，不能称作真实 Agent 任务成功率 100%。详细 hash 和命令见 [基线报告](../test/2026-11-08-agent-quality-baseline.md)。

## 5. 上下文与用户反馈（AG-04）

Profile 使用已有共享分区组装计划、上游输入、Memory/Wiki 与冻结契约；模板变量去重，覆盖变量时保留原证据，参考资料不提升为系统指令。专业修复使用实际模型的窗口与持久化会话；阶段分支、revision、owner 和并行任务隔离。长材料先进入统一预算，不经旧固定字符截断；材料选取器自身的资源/节选限制仍保留。

摘要来源 hash 参与恢复，压缩成功后生成失败仍保留已提交摘要；编辑或缩短历史会重建。请求缓存纳入实际模型版本、预算与选项，窗口缩小会重新预检。失败的上下文引用、预算、Memory/Wiki 来源和修复快照同样经 execution fence 写回。

现有设置界面增加 execution mode、模型步和工具调用上限、原生工具能力声明，以及 template/model/task 评测入口。执行侧栏展示真实运行阶段、步数/工具预算、恢复额度、HTTP 尝试、失败分类、停止原因和待审 replan。旧任务缺这些诊断时不推测生成。费用未知显示“费用未知”；Claim Evidence 图可展开冻结预期、模型建议、后端观测、比较结果与待审状态。私有检查点消息和原生密文不进入诊断 API。

## 6. 配置、兼容与迁移

| 配置/字段 | 缺省与行为 |
|---|---|
| `AgentProfile.execution_mode` | 缺省 `single`；显式 `tool_loop` 才启用循环 |
| `max_steps` / `max_tool_calls` | 循环缺省 6 / 12，合法范围 1–64 / 1–128；已有或 API 创建的 single Profile 保留既有步数兼容值 |
| 模型 `capabilities.tool_calling` | 缺省 unknown，必须确认 yes；Responses 支持不代表自动支持工具或原生压缩 |
| `AGENT_RUN_STORE_DIR` | 缺省模型配置目录下 `agent-runs`；与原始上下文一样需要持久化和敏感数据管理 |
| `AGENT_CLASSIFIED_RECOVERY` | 缺省开启；`false` 使用兼容分类决策，仍受共享恢复额度限制 |
| `MEMORY_PROCEDURE_PROMOTION` | 缺省关闭；显式 `true` 激活单案例候选，trust 仍 inferred，不能推导为通用可靠策略 |
| 数值合同与新诊断字段 | 可选扩展；旧记录可读取，缺字段不被推断为已验证 |

旧 Profile 不自动改成自由循环；专业 Benchmark/AutoResearch 不统一改为 Profile 循环。旧 claim 缺冻结合同/原件时保守降级，不能回滚为模型自报通过。上下文键和缓存键变化后的第一次调用从保留的业务原件重建；不删除原始证据或旧 generations。

## 7. 验收映射与实测范围

原方案 A01–A26 仍是验收目标；下表列本轮实际证据类别，不能用单元测试覆盖代替真实模型/沙箱任务结果。

| 原方案用例 | 本轮证据与边界 |
|---|---|
| A01–A06 | Profile/Invoker/Scheduler/协议确定性回归覆盖授权工具闭环、schema、空 allowlist、伪审批、步数/调用上限、确认结果续跑、未知副作用阻塞、审批恢复及 execution fence；使用本地模型 fixture |
| A07–A09 | 共享额度、HTTP 故障注入/退避、分类和无进展回归，以及专业 repair hook；真实依赖环境恢复成功率未测试 |
| A10–A11 | 受审 replan 提案、冻结边界及现有 revision 保护；不声称完成运行中全自动安全切图和选择性结果复用 |
| A12–A15 | 冻结原件与来源、独立指标重算、单位/容差/seed/非法观测正反例和旧 golden 假成功拒绝；未复现真实论文数值 |
| A16–A18 | 实际模型预算、上下文桩、摘要后失败重启、来源变更、revision/并行分支、原生工具 continuation 与 compaction fixture；未验证本轮真实服务商能力 |
| A19–A21 | 模型假通过修复、真实生产运行器+native 工具、冻结任务 grader、error/skipped 和零分母；未运行真实模型质量对照 |
| A22–A24 | episode/procedure 区分、真实动作证据、兼容过滤、复用记录降级、本次错误和独立案例计数回归 |
| A25 | owner/兼容过滤与参考答案隔离确定性检查；memory on/off 质量、成本、恢复收益未运行 |
| A26 | 持久化计数、重启/重跑共享额度与 guard 回归；完整长任务的费用/延迟对照没有样本 |

所有构建、类型检查和测试均在 Docker 执行；未在宿主机运行项目测试。2026-11-09 提交前对当前源码复验：后端使用挂载源码的 `agent-pr-tests`，前端使用 `node:22-bookworm-slim` 容器 `agent-pr-frontend`，将当前前端源码复制到容器 `/tmp` 后按锁文件 `npm ci`，构建产物与日志不写回工作区。

| 2026-11-09 复验 | 结果 |
|---|---|
| 后端 `go test -count=1 ./...` | 通过 |
| 后端 `go vet ./...` | 通过 |
| 后端 `go test -race -count=1 ./...` | 通过，包含最终 procedure 并发事务修复；完整日志未发现失败或数据竞争 |
| 后端覆盖率 `go test -count=1 -coverprofile=... ./...` | 通过，总语句覆盖率 61.2%，超过 CI 的 50% 门槛 |
| 后端安全回归与 Docker 镜像构建 | 通过 |
| docker-core、desktop、docker-sandbox：`go test -count=1 ./...` 与 `go vet ./...` | 按 CI workspace 模式全部通过 |
| desktop、docker-core、tool：Windows amd64 交叉编译 | 在 Linux 容器中全部通过 |
| 前端 `npm run build`（含 TypeScript）与 `npm run check:bundle` | 通过 |
| 前端 `npm run lint` | 通过 |
| 前端 `npm test` | unit 21/21；组件 43 个文件、165/165 项测试通过 |
| 提交文件的 UTF-8/mojibake gate | 通过，覆盖 tracked 与非 ignored 新增文件构造的干净提交树 |

前端旧依赖卷缺少可解析的传递依赖，初次检查未能启动；在容器内按锁文件重新安装后通过上述全部检查。工作区全量 UTF-8 扫描另发现 10 份历史 ignored `reports/` 日志含替换符或非法编码，未改动历史原件；干净提交树通过同一检查脚本。既往报告对失败 race 的 registry 归因缺少完整证据，本轮不延用该推断，以上只报告本轮实测。

本轮未运行付费真实模型、真实科研沙箱全任务、真实仓库/隐藏 holdout 或 memory on/off 对照。任务成功率、恢复成功率、长任务约束保留率、记忆净收益和 p50/p95 延迟均无新样本可报告。2026-11-07 的 [真实上下文测试](../test/2026-11-07-real-context-compression.md) 是上一批证据，不作为本轮新增六线的真实运行验收。

## 8. 运行服务与清理

2026-11-08 的 Compose 服务健康检查属于当时的环境验证，不代表运行镜像已经应用本轮源码。2026-11-09 复验启动 Docker 后使用独立测试容器和新构建的验证镜像，未更新正式应用服务。临时测试容器、日志与编译产物在提交前清理；正式运行数据和既有依赖缓存保留。

历史 `tmp/` 的宽范围删除此前被执行工具安全策略拦截，不能将加入 ignore 描述为已经删除。最终清理范围和整体检查结果由集成收口记录，不把被忽略的历史文件纳入代码交付。

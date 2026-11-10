# 共享恢复与修复记忆实施说明

日期：2026-11-08；分支：`feature/agent`。对应 [Agent 能力方案](2026-11-07-agent-capability-improvement.md) AG-02、AG-06；实现与验证以本文和当前代码为准。

六条工作线的汇总交付与验收见 [六线实施与验收](2026-11-08-agent-capability-implementation.md)。

## 恢复总账与执行边界

`models.RecoveryState` 是逻辑任务的持久恢复链，包含链 ID、已用/最大恢复动作、实际 HTTP 尝试、失败指纹、决策、动作和证据。默认最多 2 次恢复动作、64 次实际模型 HTTP 请求；正常生成、摘要、原生压缩与计数都计入 HTTP 请求。第二次及后续 HTTP 尝试、专业局部修复、调度器节点重跑共用恢复动作额度。普通 AutoResearch 候选搜索是正常实验，不因增加候选而消耗错误恢复额度。

Scheduler 的 `recoveryContext` 在第一次派发前保存初始总账。每次 `Reserve`、HTTP 尝试和动作记录先在执行 ID/epoch/lease 的 fence 下持久化，再发起操作；失败提交不会派发。HTTP 层重试消耗额度后，外层不会从零获得另一套额度。调度器的 `PendingRetry` 防止预约后重复扣费；手动重跑即使重置 `RunCount`，仍检查原恢复链。审批等待通过 `Waiting` 标记续接，不扣修复次数。

已接入共享额度的路径：`llmclient` 传输重试、Chat Completions/Responses 的实际 HTTP 派发、Coder 自修复、依赖修复、Benchmark 生成输出修复和 adapter 修复、Paper Debug 修复，以及外层节点重跑。修复会话保留自己的上下文分支；模型与工具权限仍由原有 Resolver、Invoker、审批和沙箱决定。

`AGENT_CLASSIFIED_RECOVERY` 默认开启。显式设为 `false` 保留旧节点的兼容重跑决策；总恢复额度、取消、缺输入/权限边界与不重复确定性失败仍生效。

## 分类、退避与重规划

后端提供 `FailureInfo`，区分瞬时故障、环境问题、非法输出、缺输入、权限、预算、取消和其他确定性错误。归一指纹结合错误类别/代码、阶段与输入、代码、契约、环境状态 hash，移除日志时间和随机临时路径。相同确定性指纹且相关状态未变时禁止再次恢复。

429/503 等瞬时错误允许在剩余额度内恢复。调度器使用持久 `NextAttemptAt`，指数退避以 500ms 开始，并遵守可用的 `Retry-After`；等待中的 ready 节点不会被误判为 DAG 死锁。传输层保留原来的有界退避，并消费同一恢复额度。`HTTPError` 只暴露状态与退避信息，不回显服务商正文。

缺数据与缺权限进入 blocked/现有审批等待，取消不重试。已完成的科学负结果继续作为 completed 执行结果处理，不因指标未达标触发恢复。专业 hook 可在额度内提出并执行受原权限允许的局部变更；未实际改变代码的 Coder 修复立即停止。

确定性/环境失败可生成一次 `ReplanProposal`，记录失败指纹、基础 revision、冻结契约 hash、建议变更及需要失效的产物。该对象明确要求人工审阅、保持验收契约，不修改 DAG、不切换 revision、不放宽目标、不复用未经 provenance 验证的旧结果。安全自动切图仍未启用。

Profile 工具循环的每次派发检查执行守卫。审批请求通过 `TaskExecutionResult.RuntimeAction` 在 fence 内持久化；同一结果携带的多个 ToolCalls 合并入计划。已确认完成的工具结果从私有 checkpoint 续接，结果未知的副作用保持 blocked。任务完成后保存 `completed_execution_id`、`completed_execution_epoch`、`completed_revision`，使清理租约后仍能核验实际来源。

## 基于实际动作的记忆

`ActionLedgerEntry` 记录动作、前后代码/环境/协议 hash、原始证据引用、执行 ID 与验证结果。Coder 的实际修复会保存前后代码和执行日志 Artifact，再提交动作账本；失败尝试保留未通过记录。超出单条证据资源边界的材料不会被假装压成完整验证证据或晋升 procedure。

`RecordTask` 优先读取本次 `result.Failure.Message` / `result.Error`，再回退旧任务错误。没有可识别修复动作的重试成功只生成 transient recovery episode。

procedure 候选必须同时具有：实际修复动作、不同的前后状态、前后验证引用、原始 Artifact 引用、执行 ID、环境 hash、协议 hash，以及通过验证的结果。默认 `pending_review`、`TrustInferred`、`independent_cases=1`，明确只支持该案例。`MEMORY_PROCEDURE_PROMOTION=true` 才允许新候选进入 active；来源记录属实不等于方法已普遍验证。重放旧结果不会重新激活已被失败复用降级的候选。

AutoResearch 不再把 campaign 的 `SearchRuns` 当作每个策略的验证次数。独立次数来自该 trial 自身成功 evaluator 结果的唯一执行 ID；缺少独立执行、实际 patch、协议或环境证据时只记 episode。具备条件的多次执行仍先形成待审候选。

检索继续执行 owner/project/session 边界，并对携带兼容要求的 procedure 精确匹配当前环境和协议；缺少当前兼容信息也不注入。`RecordProcedureUse` 分别记录 retrieved / adopted / executed / verified 与 unknown/success/failure，不把检索命中当成采用成功。调度入口记录实际 retrieved；任务输入 `adopted_procedure_ids` 可显式选择本次实际检索到且仍兼容的候选，后端记录 adopted。Coder 实际修复账本带该引用并保存前后原件，再由结果边界记录 executed 和 verified。没有实际动作账本时保留 unknown，未从自由文本自动推断。验证失败会降回 pending 并保留历史。记忆内容继续经过既有脱敏和注入检查，记忆中的命令不获得执行权限。

2026-11-09 复核修复：uses 追加、执行/阶段去重和失败验证的降级合并为单次存储事务，避免并发读改写丢失记录；兼容环境/协议属性保持不变。新增并发回归在关闭并重开存储后验证每个独立使用记录、版本数和失败降级状态，memory 包完整 race 通过。

## 验证与实际边界

全部测试在 `agent-capability-tests` 容器执行。确定性回归覆盖：HTTP 429 后成功、持续 503 的共享上限、HTTP/局部/外层不相乘、持久化失败与取消零派发、指纹去噪、无进展停止、缺输入/权限/取消分类、replan 仅提案、退避和 Retry-After、手动重跑不重置总账、执行 fence、完成来源 stamp、原始修复证据先保存、纯重试只 episode、本次错误优先、trial 独立次数、兼容/不兼容与跨 owner 检索、失败复用降级。

另运行实际 ProfileRunner → Scheduler → Invoker → 文件工具的审批集成：第一次请求等待审批且没有副作用；重启 checkpoint 存储后批准并续接；只执行一次文件写入、模型总生成两次、ToolCall 只保存一次、审批等待不消耗恢复额度。显式空 allowlist 经模型 JSON 与 PlanStore clone 后仍为空，不能恢复为未约束集合。

这些是本地协议、真实容器内确定性工具和故障注入结果；未据此宣称付费模型长研究任务成功率提升、跨环境修复方法普适，或 memory on/off 科研收益已有统计结论。自动重规划执行及跨不同节点 ID 的语义恢复链合并未启用，仍需已有审阅与任务 provenance 决策。

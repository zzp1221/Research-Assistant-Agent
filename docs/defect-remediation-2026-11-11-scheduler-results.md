# 2026-11-11 调度等待态、失败传播与部分成功修复报告

状态：已实施，并通过两轮独立复核整改（v3）。对应方案：[`defect-remediation-2026-11-11-scheduler.md`](defect-remediation-2026-11-11-scheduler.md)。

> 修订说明：
> - v2：第一轮独立复核 [`2026-11-11`](../reports/scheduler-independent-review-2026-11-11/review.md) 指出 v1 报告不成立之处（SCH-04 的 partial 判定、手动与自动派发共用检查、所有产物输入均已过滤），已逐项修复。
> - v3：第二轮独立复核 [`2026-11-12`](../reports/scheduler-independent-review-2026-11-12/review.md) 指出这些修复的相邻路径仍有 6 项遗漏（并发手动执行、pending 问题、同 revision 重试认证、claims 阈值可信度、API 惰性超时收尾、required_task_ids 被跳过），本文档已按实际行为重写对应表述。上轮"17 项断言全绿"只代表已纳入用例的边界，不代表机制整体完成。

## 1. 结论摘要

| 编号 | 状态 | 证据 |
|---|---|---|
| SCH-01 等待被误传播为上游失败 | 已修复 | `scheduler/result_transition_test.go`、`scheduler/tool_approval_wait_test.go`、`scheduler/terminal_wait_test.go` |
| SCH-02 终态竞态与空 Outcome | 已修复 | `scheduler/terminal_reconcile_test.go` |
| SCH-03 阻断原因缺失 | 已修复 | `scheduler/blocked_reason_test.go`、`api/plan_blocked_recovery_test.go`、`frontend/test/components/planOutcome.test.ts` |
| SCH-04 `OutcomePartial` 未生成 | 已修复 | `scheduler/partial_outcome_test.go`、`planner/plan_acceptance_test.go`、`frontend/test/components/GraphPanelOutcome.test.tsx` |
| SCH-05 恒真的 policy 开关 | 已移除 | `policy.go` 删除，结果为 `taskAllowedRetry` + `classifyTaskResult` + `dispatchLegality` |

## 2. 第一批：等待态与失败传播

- 新增 `applyTaskResult`（`internal/scheduler/task_transition.go`），自动派发与手动单节点入口共用同一套结果分流：等待、完成、`blocked`、`failed`、`canceled`、未知协议错误。
- 等待迁移要求存在 pending 审批或问题记录；裸 `awaiting_*` 返回协议错误，不写状态。
- 等待不改写 `Task.Error`、不写 `FinishedAt`、不产生 `Failure`/重试/replan，恢复检查点置 `Waiting=true、PendingRetry=false`，并释放执行租约。
- 执行围栏覆盖问题记录：`applyQuestionWaiting` 校验 `ExecutionID/LeaseOwner/ExecutionEpoch`。
- 执行异常优先级：带 Question/RuntimeAction 的失败结果仍按失败处理，等待记录不落库。
- `markTaskFailed` 不再写等待；异常附带的 `RuntimeAction` 仅保存在 metadata 供诊断。
- 工具审批：`executeToolCall` 把 `tool_approval_required` 分类为 `awaiting_approval` 并附带待审批动作，因此"请求工具审批 → 批准 → 下游继续"成为真实链路，而不是失败。
- `policy.go` 中恒真函数删除；重试许可改为 `taskAllowedRetry`（尊重禁用节点）。
- 派发围栏：`markTaskStarted` 接受 `dispatchGuard`，在获取租约的同一个 `UpdatePlan` 内重新确认节点可执行、硬依赖满足、产物可用、无待审批动作；手动入口用同一 `dispatchLegality` 判断。

## 3. 第一批：终态裁决

- `reconcilePlanState`（`internal/scheduler/scheduler_reconcile.go`）成为唯一裁决入口：外层快照只决定何时尝试，真正判断在 `UpdatePlan` 内的当前图上完成。
- 顺序：保留显式停止 → 可晋升节点 → 资源不足（有 ready 但未派发）继续等待 → 合法等待/重试/暂停 → 全终态汇总 → 真正死锁。
- 死锁分支先持久化未满足节点、依赖 ID、缺失产物和 `unsatisfied_dependencies`/`missing_artifacts` 原因，再判 `failed/failed`。
- 终态评估值一次提交 `Status / Outcome / OutcomeReason / OutcomeDetails / FinishedAt`；终态副作用只在提交成功的迁移上执行一次。
- finalizer 不再只在"无失败"时调用；其判定顺序为：停止语义 → 核心失败 `failed` / 核心阻断 `blocked` → 证据不足 `needs_review` → mock/smoke → 验收合同 → 仅可选损失 `partial` → `completed`。
- 预算顺序：全图已终结时按验收结果汇总；仍有可运行工作时才 `CancelPlan("run budget exceeded")`。运行中已触发的取消不改为 partial。

## 4. 第一批：阻断原因与恢复

- `TaskNode.block_info`（`internal/models/block_info.go`）：`code/message/causes[]/occurred_at`，`causes[]` 含 `root_task_id / via_task_id / revision / execution_epoch / status`。多个根因全部保留。
- 传播在同一个 `UpdatePlan` 内完成（`propagateBlockToDependents`），并跳过 completed/running/awaiting 节点；遇到按硬依赖不应运行的运行中节点记录一致性错误。
- 硬依赖只有一处定义（`hardDependencyIDs` = `Dependencies` + `data` 边），ready 判断、初始状态、传播、恢复共用。
- 恢复按根因重算：`refreshBlockedNodes` 丢弃已重试/已恢复根因旧轮次的 cause；仍有其他 cause 时保持 blocked；全部消失才回 `pending`。升级前无原因的 blocked 节点不自动放行。
- API 审批与问题回答通过 `scheduler.RecoverBlockedDependents` 复用同一规则，并发布 `task_unblocked` / `task_block_reason_updated`。
- 前端：`GraphPanel` 显示被阻断节点数与 `outcome_details` 横幅；`ExecutionSidebar` 显示 `block_info` 根因列表与可选性；SSE `task_blocked` 直接带结构化原因并可刷新持久化。

## 5. 第二批：partial 合同

- 新字段：`TaskNode.optional`、`TaskNode.optional_dependencies`、`PlanGraph.acceptance`、`PlanGraph.outcome_details`，均为可选 JSON 字段，旧计划缺省 `false`/空，维持严格依赖。
- 边类型新增 `optional`；规划器环检测、API 校验都同时覆盖硬/可选关系，禁止同一依赖同时为两类、禁止跨类成环、禁止可选生产者的产物成为必需输入。
- 验收合同校验：至少一个核心目标、目标必须有可验证证据（产物键或 `min_verified_claims`）、全部任务可选时拒绝、`required_task_ids` 不得指向可选任务。
- ready 判断：`optional_dependencies` 只在所指节点终结后满足；`awaiting_*` 不是终态，消费者不会越过待审批任务。
- 可选生产者的产物不被当作可信输入：调度侧 `requiredArtifactsSatisfied` 拒绝，`ContextBinder` 也不把"仅由可选依赖生产"的必需产物或失败生产者的值绑定成输入（`internal/planner/context_binder_optional_test.go`）。可选输入缺失时显式不注入，不补造数值。
- `outcome_details` 记录已通过的核心目标、可选损失、缺失产物与限制说明；partial/blocked/needs_review 在界面、通知、导出和记忆中都不再显示为全部成功。

## 6. 测试与回归

新增测试文件（v2 在末尾追加了复核整改用例）：

- `internal/scheduler/result_transition_test.go`（等待不连坐、围栏、异常优先、裸等待协议错误、未知状态）
- `internal/scheduler/terminal_reconcile_test.go`（快照时序、失败上游、真死锁、合法等待、显式停止、finalizer 门槛、资源不足）
- `internal/scheduler/blocked_reason_test.go`（多根因、旧轮次、遗留无原因、根因自带原因、传递传播、等待不传播、不覆盖运行中）
- `internal/scheduler/partial_outcome_test.go`（partial、核心失败、旧计划严格、证据不足、smoke、可选等待、可选产物不可信）
- `internal/scheduler/tool_approval_wait_test.go`（真实工具注册表：审批请求 → 等待 → 批准 → 执行）
- `internal/scheduler/terminal_wait_test.go`（批准后恢复完成、陈旧结果、独立分支）
- `internal/scheduler/dispatch_and_round_test.go`（派发围栏拒绝陈旧派发、终态副作用只执行一次、重跑清除旧结果、失败产物不可信、可选消费者随生产者重跑失效、被拒派发不泄漏预算预留）
- `internal/planner/plan_acceptance_test.go`（可选合同校验、环、必需产物、验收无核心）
- `internal/api/plan_acceptance_test.go`、`internal/api/plan_blocked_recovery_test.go`
- `internal/planner/context_binder_optional_test.go`（可选生产者的产物不可作为可信输入；失败生产者的诊断值不注入）
- `internal/scheduler/terminal_rules_test.go`（复核 11 项断言的正式版本：核心 blocked/canceled、无合同 partial、诊断证据、手动入口、问题超时、needs_review、block message、显式绑定）
- `internal/scheduler/acceptance_evidence_test.go`（失败生产者不满足必需产物、旧 revision 证据不采信、已证成合同 + 可选损失仍为 partial、严格计划仍可 completed）
- `internal/api/plan_task_wait_entry_test.go`（单节点执行入口对待审批/待回答任务返回 409 且不取租约）
- `internal/scheduler/dispatch_boundary_test.go`（第二轮复核 6 项：并发租约、pending 问题、同 revision 重试、claims 阈值、惰性超时收尾、required 任务跳过）
- `internal/scheduler/dispatch_idempotence_test.go`（重复超时只收尾一次、重试使产物失效并留事件、问题等待阻断租约、有效租约阻止二次派发）
- 前端 `test/components/planOutcome.test.ts`、`test/components/GraphPanelOutcome.test.tsx`，并扩充 `PlanCompletionNotification.test.tsx`

## 7. 执行记录

环境：Windows amd64、Go 1.27.3、Node 24.14.0、本机 `core.autocrlf=true`、`core.symlinks=false`。

| 命令 | 结果 | 记录 |
|---|---|---|
| `go build ./...` | 通过 | — |
| `go vet ./internal/scheduler ./internal/planner ./internal/api ./internal/memory ./internal/tools` | 通过 | — |
| `go test -count=1 ./internal/scheduler ./internal/planner ./internal/api ./internal/memory ./internal/tools` | planner/api/memory/tools 通过；scheduler 仅 4 个环境用例失败 | `reports/scheduler-backend-tests-2026-11-12.txt` |
| `go test -race -count=1`（scheduler 除环境用例 + planner/api/memory/tools） | 全部通过 | `reports/scheduler-backend-race-2026-11-12.txt` |
| `go test -race -count=1 ./internal/scheduler -run '<本轮回归集>'` | 通过 | `reports/scheduler-backend-race-scheduler-2026-11-11.txt` |
| `go test -count=1 -skip '<4 个环境用例>' ./internal/scheduler` | 通过（除环境用例外全绿） | `reports/scheduler-backend-tests-skipping-env-2026-11-12.txt` |
| 第一轮复核 overlay（11 项） | 11 PASS / 0 FAIL | `reports/scheduler-review-rerun-2026-11-12.txt` |
| 第二轮复核 overlay（6 项，合计 17 项） | 17 PASS / 0 FAIL | `reports/scheduler-review2-rerun-2026-11-12.txt` |
| 方案 §9 对照 + 复核对应用例 | 59 PASS / 0 FAIL | `reports/scheduler-acceptance-matrix-2026-11-11.txt` |
| `gofmt -l` / `go vet` | 无输出 / 通过 | — |
| `npm test`（frontend） | 58 文件 / 500 用例全部通过 | `reports/scheduler-frontend-tests-2026-11-12.txt` |
| `npm run build`（frontend） | 通过 | `reports/scheduler-frontend-build-2026-11-12.txt` |
| `npm run lint`（frontend） | 通过 | `reports/scheduler-frontend-lint-2026-11-12.txt` |
| `node research-assistant/test/scheduler-acceptance.mjs` | PASS | `reports/scheduler-acceptance-2026-11-12.json` |

scheduler 中失败的 4 个用例属于主机环境限制（CRLF 检出与无符号链接权限），在 `git stash` 后的基线上完全相同，详见 `reports/scheduler-baseline-failures-2026-11-11.md`。

### 7.1 在线验收（真实后端）

验收服务使用本轮源码构建（`go build -o /tmp/ra-acceptance.exe ./cmd/api`），独立端口 18099、独立存储目录 `tmp/acceptance-1111`，未启用任何模型 Key，未执行真实实验；脚本在 `finally` 中删除创建的计划。

**A：等待审批 → 批准 → 下游继续**

| 断言 | 实测 |
|---|---|
| 计划进入 `awaiting_approval` | `planStatus=awaiting_approval`，`outcome` 为空 |
| 上游节点等待而非失败 | `upstream=awaiting_approval`，无 `failure`、无 `finished_at`、无 `error` |
| 后继保持 pending、无阻断原因 | `dependent=pending`，`dependentBlockInfo=null` |
| 等待期间无失败/阻断事件 | `taskFailedEvents=0`、`taskBlockedEvents=0` |
| 批准后两个节点都完成 | `{wait-1: completed, wait-2: completed}`，`outcome=completed`，无 `task_failed` |

**B：核心成功 + 可选失败 → partial**

| 断言 | 实测 |
|---|---|
| 新合同可导入并保留 | `acceptance` 原样返回，`optional=true`，`optional` 关系边存在 |
| 节点结果 | `{core-1: completed, opt-1: failed, report-1: completed}` |
| 计划终态 | `planStatus=completed`、`outcome=partial` |
| 有核心验收依据 | `accepted_goals=[{id:g1, verified:true, evidence_refs:[tool_result]}]` |
| 有明确损失 | `optional_losses=[{task_id:opt-1, name:可选工具, status:failed, reason:tool_execution_failed…}]` |
| 有结论限制说明 | `limitations=[缺少可选工作 optional tool，相关结论不能宣称完整]` |
| 事件与快照一致 | 终态事件 `plan_completed.payload.outcome=partial` |
### 7.3 已知边界

- `repo_prepare_test.go` 的 4 个失败用例需在 `core.symlinks=true` + `core.autocrlf=false` 的 Linux/管理员主机上复核；本轮未修改该文件。
- `PlanGraph.acceptance` 的自动填充（由 planner agent 生成合同）仍使用现有模板/agent 输出，未强制每个新计划都生成合同；旧计划继续按严格必需处理。
- 历史 `Outcome=""` 的补全只在能从状态与事件明确重建时执行（`reconcilePlanState` 的防御分支）；无法判定时不改写历史，保持"历史结果未记录"。

## 9. 方案 §9 用例对照

表中 44 个用例可单条命令执行，当前全部通过（44 PASS / 0 FAIL），记录见 `reports/scheduler-acceptance-matrix-2026-11-11.txt`。

| 用例 | 覆盖测试 |
|---|---|
| A-01 前置审批 pending | `TestWaitingApprovalDoesNotBlockDependents`、`TestApprovedWaitResumesAndCompletes` |
| A-02 执行中请求工具审批 | `TestToolApprovalRequestPausesInsteadOfFailing`、`TestApprovedWaitResumesAndCompletes` |
| A-03 批准后恢复且不重复副作用 | `TestToolApprovalRequestPausesInsteadOfFailing` |
| A-04 结构化问题等待 / 恢复 | `TestWaitingInputDoesNotChargeRetryOrBlock`、`TestSchedulerQuestionWaitKeepsDependentsPending` |
| A-05 陈旧执行返回 Question / RuntimeAction | `TestStaleExecutionCannotPauseTask`、`TestStaleResultCannotTouchWaitingRecords` |
| A-06 仓库缺失 / 数据形状不匹配 | `TestBlockedRootCarriesItsOwnReason`、`TestPlanFinalizerIsInvokedForBlockedGraphs` |
| A-07 真实失败可重试 / 不可重试 | `TestRealFailureBlocksDependentsWithReason`、`TestRetryDoesNotBlockIndependentBranch`、`TestTransientRetryHonorsBackoffAndCompletionSavesProvenance` |
| A-08 快照后下游全部 blocked | `TestTerminalRaceWritesOutcomeAfterSnapshotGap` |
| A-09 failed 上游 + pending 后继 | `TestFailedUpstreamStillProducesTerminalOutcome`、`TestRealDeadlockRecordsUnsatisfiedNodes` |
| A-10 合法 awaiting / 延迟重试 / 暂停 | `TestAwaitingPlanIsNotDeadlock`、`TestPausePlanStopsDispatchAndResumeCompletes` |
| A-11 无法满足的依赖 / 缺失产物 | `TestRealDeadlockRecordsUnsatisfiedNodes` |
| A-12 拒绝、审批超时、问题超时 | `TestRuntimeActionRejectionBlocksPlan`、`TestExpireQuestionsWithoutDefaultBlocks`、`TestQuestionTimeoutFinalizesPlanOnce` |
| A-13 多次唤醒 / 事件重放 | `TestTerminalSideEffectsRunOnce` |
| A-14 快照刷新 / SSE 重连可见阻断原因 | `TestBlockedNodeCarriesReadableMessage`、`planOutcome.test.ts`、`GraphPanelOutcome.test.tsx` |
| A-15 两上游只重试其一 | `TestTwoRootsKeepBothCausesWhenOneIsRetried` |
| A-16 cancel / retry / reset / 重新进入等待 | `TestRetryClearsPreviousOutcomeAndReexplains`、`TestCancelledStopReasonIsPreserved`、`TestResetFromNodeInvalidatesOptionalConsumers` |
| A-17 ready 但并发槽耗尽 | `TestRunnableButNotDispatchedIsNotATerminalState` |
| A-18 选出 ready 后节点被阻断 | `TestDispatchGuardRefusesBlockedNodeBeforeLease`、`TestRefusedDispatchReleasesBudgetReservation`、`TestManualEntryRespectsPendingApproval` |
| B-01 核心成功 + 可选失败 | `TestOptionalFailureYieldsPartialWithLosses`、`TestProvenContractWithOptionalLossStaysPartial`、在线验收 B_PARTIAL |
| B-02 核心失败不得 partial | `TestCoreFailureIsNeverPartial`、`TestTerminalCoreBlockedBeatsPartial` |
| B-03 证据不足 needs_review | `TestDegradedEvidenceIsNeedsReviewNotPartial`、`TestNeedsReviewKeepsExecutionCompleted` |
| B-04 旧计划无 optional | `TestLegacyPlanWithoutOptionalFieldsStaysStrict`、`TestStrictPlanWithoutContractCanStillComplete` |
| B-05 可选任务被核心引用 / 混合成环 | `TestValidatePlanGraphRejectsOptionalProducerOfRequiredArtifact`、`TestValidatePlanGraphRejectsOptionalCrossCycle`、API 同名用例 |
| B-06 仅可选依赖 / awaiting 可选任务 / 禁用节点 | `TestOptionalConsumerWaitsForOptionalProducerToEnd`、`TestFillInitialStatusesHonoursOptionalDependencies` |
| B-07 全部可选 / 无验收依据 | `TestValidatePlanGraphRejectsAcceptanceWithoutCore`、`TestValidatePlanGraphRejectsAcceptanceWithoutEvidence`、`TestTerminalNoContractCannotBePartial` |
| B-08 smoke / mock 保留且列损失 | `TestSmokePlanKeepsSmokeWithOptionalLoss`、`TestTerminalCoreBlockedBeatsSmoke` |
| B-09 失败任务留下同名产物 | `TestOptionalProducerArtifactIsNotATrustedInput`、`TestFailedProducerArtifactIsNeverAFreePass`、`TestContextBinderRefusesOptionalOnlyArtifact`、`TestExplicitBindingRejectsFailedOptionalArtifact`、`TestFailedProducerNeverSatisfiesRequiredArtifact` |
| B-10 最后一次 attempt / 尚有剩余工作 | `TestExhaustedAttemptBudgetDoesNotOverwriteTerminalSummary`、`TestExhaustedAttemptBudgetWithRunnableWorkCancels` |
| B-11 revision / 导入导出 / clone / 重跑 | `TestPlanDiffReportsOptionalAndAcceptanceChanges`、`TestResetFromNodeInvalidatesOptionalConsumers`、`TestSupersededRevisionEvidenceIsNotAccepted`、在线 B_IMPORTED |
| B-12 partial / blocked / needs_review 的 UI 与通知 | `GraphPanelOutcome.test.tsx`、`PlanCompletionNotification.test.tsx`、`planOutcome.test.ts` ## 8. 独立复核整改

### 8.1 第一轮复核（2026-11-11）：9 类功能遗漏

复核文件：[`reports/scheduler-independent-review-2026-11-11/review.md`](../reports/scheduler-independent-review-2026-11-11/review.md)。11 项断言在整改前全部失败，整改后全部通过，并固化为 `internal/scheduler/terminal_rules_test.go`。

| 编号 | 问题 | 修复 | 正式回归用例 |
|---|---|---|---|
| R1 | 核心 blocked 被 partial / smoke 覆盖 | 终态判定先算核心准入，早于所有成功分支 | `TestTerminalCoreBlockedBeatsPartial`、`TestTerminalCoreBlockedBeatsSmoke` |
| R2 | 核心 canceled 被当作完成 | `CoreStopped` 纳入 canceled 与未终结 | `TestTerminalCoreCanceledIsNotCompleted` |
| R3 | 无验收合同仍产生 partial | partial 需合同 + 目标证成 + 可选损失 | `TestTerminalNoContractCannotBePartial` |
| R4 | 失败任务的诊断产物被当作证据 | `planner.TrustedArtifact` 统一判定 | `TestTerminalDiagnosticArtifactNeverVerifiesGoal` 等 |
| R5 | 显式 InputBinding 绕过过滤 | artifact 绑定走同一信任判定 | `TestExplicitBindingRejectsFailedOptionalArtifact` |
| R6 | 手动执行可启动待审批节点 | `dispatchLegality` + 租约内复检 + API 409 | `TestManualEntryRespectsPendingApproval`、`TestExecuteTaskRejectsPendingWaits` |
| R7 | 协议错误后仍占租约 | 非陈旧错误提交失败并释放租约 | `TestManualProtocolErrorReleasesLease` |
| R8 | 问题超时跳过终态收尾 | 超时走 `finishExternalTerminal` | `TestQuestionTimeoutFinalizesPlanOnce` |
| R9 | needs_review 被映射为失败 | 恢复 `completed/needs_review` | `TestNeedsReviewKeepsExecutionCompleted` |
| 细节 | `BlockInfo.Message` 为空 | 用首因补全 | `TestBlockedNodeCarriesReadableMessage` |

### 8.2 第二轮复核（2026-11-12）：6 项相邻路径遗漏

复核文件：[`reports/scheduler-independent-review-2026-11-12/review.md`](../reports/scheduler-independent-review-2026-11-12/review.md)。6 项断言在整改前全部失败，整改后 `17 PASS / 0 FAIL`（含第一轮 11 项），并固化为 `dispatch_boundary_test.go` 与 `dispatch_idempotence_test.go`。

| 编号 | 问题 | 修复 | 正式回归用例 |
|---|---|---|---|
| V2-01 | 并发手动请求可让同一节点执行两次 | `dispatchLegality` 在租约事务内拒绝 `in_progress` 与未失效租约（`taskHasLiveLease`） | `TestConcurrentManualTakesOnlyOneLease`、`TestLiveLeaseBlocksSecondDispatch` |
| V2-02 | 租约提交前新增 pending 问题仍能启动 | 共享谓词纳入 pending `PlanQuestion` 与 `awaiting_*`，并拒绝处于等待态的 plan | `TestLeaseRefusesTaskWithPendingQuestion`、`TestQuestionWaitBlocksLeaseAcquisition` |
| V2-03 | 同 revision 重试后旧诊断产物重新生效 | 产物记录 `metadata.execution_id`；`TrustedArtifact` 要求与生产者 `completed_execution_id` 一致；重试/改派使该节点产物失效并发布 `artifacts_invalidated` | `TestRetryCannotRecertifyOldArtifact`、`TestRetryInvalidatesProducedArtifacts` |
| V2-04 | `min_verified_claims` 绕过证据图可信度 | `trustedClaimEvidenceGraph` 对证据图应用同一生产者/revision/execution 规则；finalizer 与 goal 明细共用 | `TestClaimThresholdRequiresTrustedGraph` |
| V2-05 | API 惰性问题超时不触发终态收尾 | `ExpireQuestionsNow` 也走 `finishExternalTerminal`，与循环共用一次性标记 | `TestLazyQuestionTimeoutRunsTerminalPipelineOnce`、`TestRepeatedQuestionTimeoutFinalizesOnce` |
| V2-06 | `required_task_ids` 指定的任务被跳过仍验收成功 | `RequiredNotCompleted` 计入核心未达成；required 任务必须 completed | `TestRequiredTaskMustActuallyComplete` |

### 8.3 复核复现命令

```text
go test -overlay reports/scheduler-independent-review-2026-11-11/overlay.json ./internal/scheduler -run '^TestReview'
go test -overlay reports/scheduler-independent-review-2026-11-12/overlay.json ./internal/scheduler -run '^TestReview'
```

| 阶段 | 第一轮 11 项 | 第二轮 6 项 |
|---|---|---|
| 整改前 | 11 FAIL | 6 FAIL |
| 整改后 | 11 PASS / 0 FAIL | 6 PASS / 0 FAIL（合计 17 PASS） |

记录：`reports/scheduler-review-rerun-2026-11-12.txt`、`reports/scheduler-review2-rerun-2026-11-12.txt`。复核脚本按"只审查不改产品代码"保留未修改；等价断言已用正式名称写入仓库测试，不依赖 overlay 也能回归。

### 7.3 已知边界

- `repo_prepare_test.go` 的 4 个失败用例需在 `core.symlinks=true` + `core.autocrlf=false` 的 Linux/管理员主机上复核；本轮未修改该文件。
- `PlanGraph.acceptance` 的自动填充（由 planner agent 生成合同）仍使用现有模板/agent 输出，未强制每个新计划都生成合同；旧计划继续按严格必需处理。
- 历史 `Outcome=""` 的补全只在能从状态与事件明确重建时执行（`reconcilePlanState` 的防御分支）；无法判定时不改写历史，保持"历史结果未记录"。

## 9. 方案 §9 用例对照

表中 44 个用例可单条命令执行，当前全部通过（44 PASS / 0 FAIL），记录见 `reports/scheduler-acceptance-matrix-2026-11-11.txt`。

| 用例 | 覆盖测试 |
|---|---|
| A-01 前置审批 pending | `TestWaitingApprovalDoesNotBlockDependents`、`TestApprovedWaitResumesAndCompletes` |
| A-02 执行中请求工具审批 | `TestToolApprovalRequestPausesInsteadOfFailing`、`TestApprovedWaitResumesAndCompletes` |
| A-03 批准后恢复且不重复副作用 | `TestToolApprovalRequestPausesInsteadOfFailing` |
| A-04 结构化问题等待 / 恢复 | `TestWaitingInputDoesNotChargeRetryOrBlock`、`TestSchedulerQuestionWaitKeepsDependentsPending` |
| A-05 陈旧执行返回 Question / RuntimeAction | `TestStaleExecutionCannotPauseTask`、`TestStaleResultCannotTouchWaitingRecords` |
| A-06 仓库缺失 / 数据形状不匹配 | `TestBlockedRootCarriesItsOwnReason`、`TestPlanFinalizerIsInvokedForBlockedGraphs` |
| A-07 真实失败可重试 / 不可重试 | `TestRealFailureBlocksDependentsWithReason`、`TestRetryDoesNotBlockIndependentBranch`、`TestTransientRetryHonorsBackoffAndCompletionSavesProvenance` |
| A-08 快照后下游全部 blocked | `TestTerminalRaceWritesOutcomeAfterSnapshotGap` |
| A-09 failed 上游 + pending 后继 | `TestFailedUpstreamStillProducesTerminalOutcome`、`TestRealDeadlockRecordsUnsatisfiedNodes` |
| A-10 合法 awaiting / 延迟重试 / 暂停 | `TestAwaitingPlanIsNotDeadlock`、`TestPausePlanStopsDispatchAndResumeCompletes` |
| A-11 无法满足的依赖 / 缺失产物 | `TestRealDeadlockRecordsUnsatisfiedNodes` |
| A-12 拒绝、审批超时、问题超时 | `TestRuntimeActionRejectionBlocksPlan`、`TestExpireQuestionsWithoutDefaultBlocks`、`TestQuestionTimeoutFinalizesPlanOnce` |
| A-13 多次唤醒 / 事件重放 | `TestTerminalSideEffectsRunOnce` |
| A-14 快照刷新 / SSE 重连可见阻断原因 | `TestBlockedNodeCarriesReadableMessage`、`planOutcome.test.ts`、`GraphPanelOutcome.test.tsx` |
| A-15 两上游只重试其一 | `TestTwoRootsKeepBothCausesWhenOneIsRetried` |
| A-16 cancel / retry / reset / 重新进入等待 | `TestRetryClearsPreviousOutcomeAndReexplains`、`TestCancelledStopReasonIsPreserved`、`TestResetFromNodeInvalidatesOptionalConsumers` |
| A-17 ready 但并发槽耗尽 | `TestRunnableButNotDispatchedIsNotATerminalState` |
| A-18 选出 ready 后节点被阻断 | `TestDispatchGuardRefusesBlockedNodeBeforeLease`、`TestRefusedDispatchReleasesBudgetReservation`、`TestManualEntryRespectsPendingApproval` |
| B-01 核心成功 + 可选失败 | `TestOptionalFailureYieldsPartialWithLosses`、`TestProvenContractWithOptionalLossStaysPartial`、在线验收 B_PARTIAL |
| B-02 核心失败不得 partial | `TestCoreFailureIsNeverPartial`、`TestTerminalCoreBlockedBeatsPartial` |
| B-03 证据不足 needs_review | `TestDegradedEvidenceIsNeedsReviewNotPartial`、`TestNeedsReviewKeepsExecutionCompleted` |
| B-04 旧计划无 optional | `TestLegacyPlanWithoutOptionalFieldsStaysStrict`、`TestStrictPlanWithoutContractCanStillComplete` |
| B-05 可选任务被核心引用 / 混合成环 | `TestValidatePlanGraphRejectsOptionalProducerOfRequiredArtifact`、`TestValidatePlanGraphRejectsOptionalCrossCycle`、API 同名用例 |
| B-06 仅可选依赖 / awaiting 可选任务 / 禁用节点 | `TestOptionalConsumerWaitsForOptionalProducerToEnd`、`TestFillInitialStatusesHonoursOptionalDependencies` |
| B-07 全部可选 / 无验收依据 | `TestValidatePlanGraphRejectsAcceptanceWithoutCore`、`TestValidatePlanGraphRejectsAcceptanceWithoutEvidence`、`TestTerminalNoContractCannotBePartial` |
| B-08 smoke / mock 保留且列损失 | `TestSmokePlanKeepsSmokeWithOptionalLoss`、`TestTerminalCoreBlockedBeatsSmoke` |
| B-09 失败任务留下同名产物 | `TestOptionalProducerArtifactIsNotATrustedInput`、`TestFailedProducerArtifactIsNeverAFreePass`、`TestContextBinderRefusesOptionalOnlyArtifact`、`TestExplicitBindingRejectsFailedOptionalArtifact`、`TestFailedProducerNeverSatisfiesRequiredArtifact` |
| B-10 最后一次 attempt / 尚有剩余工作 | `TestExhaustedAttemptBudgetDoesNotOverwriteTerminalSummary`、`TestExhaustedAttemptBudgetWithRunnableWorkCancels` |
| B-11 revision / 导入导出 / clone / 重跑 | `TestPlanDiffReportsOptionalAndAcceptanceChanges`、`TestResetFromNodeInvalidatesOptionalConsumers`、`TestSupersededRevisionEvidenceIsNotAccepted`、在线 B_IMPORTED |
| B-12 partial / blocked / needs_review 的 UI 与通知 | `GraphPanelOutcome.test.tsx`、`PlanCompletionNotification.test.tsx`、`planOutcome.test.ts` |
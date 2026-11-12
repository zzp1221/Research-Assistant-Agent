# 2026-11-12 调度终态、产物可信与验收边界修复报告（第三次复核整改）

状态：已实施，待独立复核。对应规格：[spec.md](spec.md)；实施计划：[plan.md](plan.md)；任务清单：[tasks.md](tasks.md)。
复核来源：[reports/scheduler-independent-review-2026-11-12/review-v3.md](reports/scheduler-independent-review-2026-11-12/review-v3.md)（2 PASS / 6 FAIL）。

> 修订说明：
> - v1：第二轮复核的 V2-01 ~ V2-06 修复后，第三轮复核指出 6 处相邻路径仍未覆盖（自动重试的产物信任、终态收尾的进程内标记、审批超时绕开统一管道、claims 统计未受信任、required 任务被禁用、`outcome_details` 与判定不一致）。本文档记录本轮整改。
> - 本轮不推翻 V2 的 6 项修复；上两轮 overlay（17 + 17 项）在整改后仍全部通过。

## 1. 结论摘要

| 编号 | 问题 | 修复 | 正式回归用例 |
|---|---|---|---|
| V3-01 | 自动重试后失败尝试的诊断产物被重新认证 | 完成/失败两条写入路径都记录 `metadata.execution_id` 与 `metadata.evidence`；`TrustedArtifact` 拒绝 `diagnostic` | `TestAutoRetryCannotRecertifyFailedDiagnostic`、`TestAutoRetryReproducingArtifactIsAccepted` |
| V3-02 | 终态收尾标记只在内存，重启后永久丢失收尾 | `PlanGraph.TerminalCommittedAt` 持久化 + `claimTerminalCommit` 原子认领 + `ReconcileTerminalCommits` 补偿（启动与读取时触发） | `TestTerminalPipelineSurvivesRestart`、`TestCancelRunsTerminalPipelineOnce` |
| V3-03 | `outcome_details` 的 claims 统计来自未受信任的图 | 统计改走 `planner.TrustedClaimEvidenceGraph`，与判定共用一次评估 | `TestOutcomeDetailsClaimsFollowTrust` |
| V3-04 | required 任务被计划禁用后仍验收成功 | `RequiredTaskIDs` 中 `enabled=false` 记为 `ContractReason`，终态为 `completed/needs_review`；`strictPlanEvaluation` 同步处理 | `TestDisabledRequiredTaskNeedsReview` |
| V3-05 | 审批超时 sweep 自建终态并吞掉一次性标记 | sweep 只提交状态与事件；新增 `ExpireApprovalsNow`；API 统一走 `sweepLazyTimeouts` | `TestApprovalTimeoutUsesOneTerminalPipeline` |

## 2. 产物可信（V3-01）

**根因**：`execution_id` 只在 `markTaskCompleted` 写入，失败路径写入的产物没有标记，`TrustedArtifact` 的绑定分支因 `stamp == ""` 被整体跳过；`RetryTask` 的失效逻辑又不覆盖调度器自己安排的自动重试。

**修复**（[`scheduler_state.go`](../research-assistant/backend/internal/scheduler/scheduler_state.go)、[`acceptance_evidence.go`](../research-assistant/backend/internal/planner/acceptance_evidence.go)）：

1. 失败路径持久化产物时写入本次失败的 `execution_id`，并标记 `evidence = "diagnostic"`；同时补上完成路径已有的 producer 冲突保护。
2. 完成路径写入 `evidence = "verified"`。
3. `TrustedArtifact` 先判 `diagnostic` 直接拒绝，再走原有的 revision 与 execution 绑定规则。

行为：自动重试不重产 → 旧诊断不可验收；重试重产后 → 正常 `completed`（`TestAutoRetryReproducingArtifactIsAccepted`）。显式 `RetryTask` 的失效行为不变。

## 3. 终态收尾持久化（V3-02）

**根因**：终态是“先提交状态、后执行副作用”的两段式，而“已执行副作用”的事实存在进程内 map。进程在两步之间退出后，`ExpireQuestionsNow` 返回 false、`reconcilePlanState` 不补跑、`RecoverInterruptedPlans` 只处理 `in_progress`，终态计划永久没有事件、预算释放与记忆记录。

**修复**：

1. `models.PlanGraph` 新增 `terminal_committed_at`（可空，旧快照为 nil）。
2. `claimTerminalCommit` 在 `UpdatePlan` 内 CAS 认领；`completeTerminalOnce` = 认领 + `finalizeTerminalCommit`，成为 `finishExternalTerminal`、`reconcilePlanState` 终态分支的唯一入口。
3. `finishPlanTerminal`、`CancelPlanWithActor` 同样先认领再发事件与副作用；`recordTerminalMetrics` 不再承担幂等职责。
4. 新执行轮次（`ExecutePlan`、`RetryTask`、`ResetFromNode`、`ReassignTask`）在同一事务内清除标记。
5. `ReconcileTerminalCommits` 补偿“已终态但未认领”的计划：API 启动时扫描一次，计划读取时按需触发。

## 4. 审批超时管道（V3-05）

**根因**：`expireApprovalActions` 自己发布 `plan_failed` 并调用 `recordTerminalMetrics`——后者正是写入一次性标记的函数。执行循环随后调用 `finishExternalTerminal` 时看到标记已存在，直接返回，导致 `onTerminal`、记忆写入与预算释放全部没有执行。

**修复**：sweep 只负责状态提交与自身事件；新增 `ExpireApprovalsNow`（与 `ExpireQuestionsNow` 对称）；API 的 `getPlan` / `listQuestions` / `listApprovals` 统一走 `sweepLazyTimeouts`，同时处理问题超时、审批超时与终态补偿。

## 5. 验收统计与合同（V3-03 / V3-04）

- `buildOutcomeDetails` 的 `claims_verified` / `claims_total` 改由 `planner.TrustedClaimEvidenceGraph` 提供；不可信图计 0/0，与 `needs_review` 判定一致。可信图仍按 `summary` 原样填充（`TestOutcomeDetailsClaimsFollowTrust` 两个分支都覆盖）。
- `EvaluateAcceptanceEvidence`：`RequiredTaskIDs` 中被计划禁用（`enabled=false`）的任务记为 `ContractReason = "acceptance requires task <id> which the plan disables"`；finalizer 与 `strictPlanEvaluation` 都映射为 `completed/needs_review`，不再静默 `partial`。

## 6. 验证

| 执行 | 结果 |
|---|---|
| 第三轮 overlay `^TestReviewV3` | 整改前 2 PASS / 6 FAIL → 整改后 **8 PASS / 0 FAIL** |
| 第一轮 / 第二轮复核 overlay | 17 PASS、17 PASS（无回退） |
| `review_v3_test.go` 正式回归 | 7/7 通过 |
| `go build ./...`、`go vet`（5 包） | 通过 |
| `go test -count=1`（scheduler/planner/api/memory/tools） | planner、api、memory、tools 通过；scheduler 仅 4 个已知 Windows 环境用例失败 |
| `go test -race`（scheduler/planner/api） | 通过 |
| 前端 `npm test` / `npm run build` | 58 文件 / 500 用例通过；构建通过 |

## 7. 已知边界

1. 失败尝试的产物仍保留在 `plan.Artifacts` 用于诊断与展示，只是不再具备验收效力；下游显式输入绑定沿用既有信任判定。
2. `terminal_committed_at` 为新增字段，历史快照为 nil；补偿会为“已终态但无标记”的历史计划补发一次终态事件，因此升级后第一次启动可能产生一批补发事件（每个计划至多一次）。
3. 4 个 Windows 环境用例（CRLF、符号链接权限）仍失败，与本次改动无关，未新建基线。
4. 审批超时事件由统一管道发布，`payload` 结构从 `{reason}` 变为 `{outcome, outcome_reason, outcome_details}`，与问题超时路径一致。

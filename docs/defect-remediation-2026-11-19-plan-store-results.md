# 计划存储隔离与提交一致性：修复与验收报告

日期：2026-11-19。状态：已实施，已完成本报告所列环境内的验证，并已复制到现有 Docker 后端容器运行。

原始修复与本地验收基线：`d4d2af1a5fda67fbd027f5d1e5cfe9116ace20fa`。本轮代码通过独立修复分支和 PR 提交；与主分支的集成及远程检查见关联 PR。设计契约见[修复方案 v1.1](defect-remediation-2026-11-19-plan-store.md)；原始审阅见[历史交接](project-assessment-handoff-2026-11-18.md)。历史评分及其他问题不因本轮修复自动更新。

本轮修复了返回对象泄漏内部引用、写盘失败后暴露未提交状态，以及 Windows 旧日志迁移失败。Linux 后端最终全量、相关 race/vet、Windows 存储及 API 验证通过；Windows scheduler 有 5 项在同环境原基线复现的失败，另有一次文献检索公网依赖测试的暂态失败，见第 4 节。在已执行范围内未发现由本次修复引入的失败，不能据此承诺所有运行条件下均无缺陷。

## 1. 实际修复与问题映射

| 问题 | 实际变化 | 主要实现 |
|---|---|---|
| PS-01、PS-05：嵌套引用共享与字段复制不全 | Save 输入、Get/List 输出、事件、摘要均独立复制；保留具体 Go 类型、整数精度、typed nil 与 nil/empty；拒绝循环及不支持对象 | [clone.go](../research-assistant/backend/internal/store/clone.go)、[memory_plan_store.go](../research-assistant/backend/internal/store/memory_plan_store.go)、[summary.go](../research-assistant/backend/internal/store/summary.go) |
| PS-02、PS-03：先发布内存、后写盘 | 文件提交确认后才发布候选状态；创建、更新、事件和删除分别有明确提交点 | [file_plan_store.go](../research-assistant/backend/internal/store/file_plan_store.go)、[file_plan_store_io.go](../research-assistant/backend/internal/store/file_plan_store_io.go) |
| PS-04：回调撤销、dirty check 与重试受污染 | 回调前后分别复制；回调失败丢弃工作对象；嵌套变更正常触发持久化；确定失败后重试可保存 | 同上及 [store_isolation_test.go](../research-assistant/backend/internal/store/store_isolation_test.go) |
| PS-06：Windows 迁移文件占用 | 迁移替换前显式关闭输入句柄；先生成并刷新完整新日志，再替换原日志 | [file_plan_store_recovery.go](../research-assistant/backend/internal/store/file_plan_store_recovery.go) |
| 调用链：未提交事件仍发布、存储错误误报 404 | 事件先存后发；失败不计 activity、不继续派发；区分 404/503；创建事件失败保留原 plan ID 和幂等键 | [scheduler_persistence.go](../research-assistant/backend/internal/scheduler/scheduler_persistence.go)、[scheduler_events.go](../research-assistant/backend/internal/scheduler/scheduler_events.go)、[plan_store_errors.go](../research-assistant/backend/internal/api/plan_store_errors.go) |

复制采用递归反射并为 `time.Time` 保留值语义。两个 store 在写入前都进行 JSON 可编码性校验。动态类型的保留针对内存复制；磁盘重开仍遵循 JSON 的既有类型归一化规则。

## 2. 提交、恢复与错误行为

计划文件使用 v2 envelope，携带 `plan_store_format`、`plan_id`、`generation`、`event_seq_floor`、`state` 和完整 `plan`。创建、更新以快照替换为权威；删除写入同路径 tombstone，提交后才移除内存视图。新版本不再追加 `plan_saved/plan_updated/plan_deleted` 生命周期记录；完整计划保存在快照，JSONL 保留业务事件。更早的 `kind: "plan"` 日志仍由兼容恢复路径读取。

业务事件完整写入并 Sync 后才发布内存与 SSE。Write、短写或 Sync 失败时尝试 Truncate 到原有效边界并再次 Sync；回退确认后才返回确定未提交。无法确认替换或回退结果时封存实例，后续计划、列表、摘要和事件读写均拒绝，必须重新打开并恢复。

| 结果 | 对外行为 |
|---|---|
| 输入、编码或回调失败 | 不提交，保留原状态；不自动带 `ErrNotCommitted` 标记 |
| 确定未提交的持久化 I/O 失败 | `ErrNotCommitted`；解除故障后可按业务幂等约束重试；API 返回 503 `store_unavailable` |
| 提交结果不确定 | `CommitError` / `ErrCommitUnknown`，附操作、plan ID、generation、Seq 与原因；API 返回 503 `store_commit_unknown` |
| 已封存实例 | `ErrStoreRecoveryRequired`；API 返回 503 `store_recovery_required` |
| 计划确实不存在 | `ErrPlanNotFound`；API 仍返回 404 `plan_not_found` |

实现细化与边界：

- JSON 持久化内容相等的 no-op 不写盘，但仍发布独立 candidate，保留内存中的具体类型与空值语义。重新打开后的比较以持久化内容为准。
- v2 active/tombstone 覆盖旧日志推导的状态；generation 隔离删除重建前后的事件；Seq floor 跨重建不倒退。逻辑删除保留 tombstone，旧事件可能仍在物理日志中。
- 发布审查补充修复：旧版本可产生 `[0,1]`、`[0,0,1]` 等含无序号事件的数据，不能给旧零 Seq 自动补号，否则会撞上已经发布的正序号。现保留 legacy 的零 Seq、正 Seq 和原顺序；零 Seq 在完整事件列表可见，增量读取仍按旧 cursor 过滤。迁移保留 legacy generation 的原事件组，新追加事件使用 v2 和递增正 Seq。
- 兼容旧整体 JSON snapshot、旧 append-only、旧 snapshot + log 和 v2 混合恢复。整体 snapshot、含完整 plan 的 append-only 启动时迁移；旧裸 snapshot + marker 路径可按需在首次保存、更新或删除时升级。
- 完整末行缺换行时补分隔符；截断尾记录修复至最后有效边界；中间损坏、非法 v2 格式和身份冲突明确拒绝，不静默回退复活旧数据。
- Windows 使用临时文件 Sync + `MoveFileEx(REPLACE_EXISTING | WRITE_THROUGH)`，没有先删旧文件的空窗；Windows 目录 Sync helper 为空。Unix 路径执行 Rename 与父目录 Sync。
- Windows 事件文件采用 `O_RDWR` 并在进程锁内 SeekEnd，保证失败后可 Truncate。另已修复大小写 ID 文件碰撞，拒绝非法及保留文件身份、负 Seq、重复或倒退的显式 Seq。
- `Health()` 已接入 scheduler 和计划运行时 API 路由闸门，尚未接入 `/api/health` 响应。HTTP 健康接口成功仅证明服务健康检查成功，不能单独证明计划存储未封存。

计划保存与随后事件追加仍是两次独立提交。计划已保存而创建事件失败时，API 保留 plan ID，创建幂等键保留为 `commit_unknown`，重试不会重新规划或创建。外部任务、记忆、审计和清理不属于同一文件事务。提交后响应前退出可能丢失确认；普通事件没有稳定事件 ID 去重协议，不保证此时重试恰好一次。

## 3. 验收矩阵与永久回归

下列名称均可在源码中查找；部分测试包含 memory/file 或故障点子用例。矩阵映射说明覆盖关系，不代表已穷尽所有文件系统故障组合。

| 设计编号 | 测试名称或文件 | 核心断言 |
|---|---|---|
| ISO-01/02 | `TestPlanStoreIsolationAcrossEntrypoints` | 修改原输入或 Get/List 返回对象不污染内部状态 |
| ISO-03/04 | `TestPlanStoreCallbackIsolationAndNestedPersistence` | 回调失败撤销；回调保留对象及赋入外部对象在成功后不泄漏 |
| ISO-05 | `TestPlanStoreNestedOnlyMutationsPersist/{memory,file}/{map,slice,pointer,raw_message}` | 每次仅修改一处嵌套内容，不改时间或其他字段，文件重开可恢复 |
| ISO-06 | `TestPlanStoreEventIsolationAndLifecycleSequence` | 输入、追加返回和事件查询输出相互独立 |
| ISO-07 | `TestPlanStoreSummaryIsolation` | 修改摘要时间、Usage、Review 不影响计划及其他摘要 |
| ISO-08/09 | `TestClonePreservesDynamicTypesAndEmptyValues`、`TestCloneRejectsCircularAndUnsupportedValues`、`TestPlanStoreRejectsInvalidCandidatesWithoutMutation` | 类型/精度/空值保持；不可复制或编码输入明确拒绝且不污染状态 |
| COM-01 | `TestFilePlanStoreNoopDoesNotTouchDisk`、`TestFilePlanStoreJSONEquivalentUpdatePublishesOwnedMemory` | no-op 无磁盘 I/O，内存候选仍独立 |
| COM-02/05 | `TestFilePlanStoreCommitKnownFailuresAndRetry` | 保存/更新/删除故障不泄漏状态，解除故障后同值重试落盘 |
| COM-03/04 | `TestFilePlanStoreProcessDeathAtConfirmedWrite`、`TestFilePlanStoreUncertainReplacementSealsAllAccess` | 确认边界中止可恢复；unknown 后全部入口拒绝 |
| EVT-01/02 | `TestFilePlanStoreEventRollbackAndRetry`、`TestFilePlanStoreEventRollbackFailureSeals` | 失败回退成功不发布；回退失败封存 |
| EVT-03 | `TestFilePlanStoreProcessDeathAtConfirmedWrite`、`TestFilePlanStoreCommittedEventCloseErrorIsDiagnostic` | 事件确认后中止可恢复；确认后的 Close 错误不反转提交结果 |
| EVT-04 | `TestPlanStoreEventIsolationAndLifecycleSequence`、`TestFilePlanStoreRejectsSequenceAndIdentityWithoutChanges` | Seq 与 PlanID 校验不改变已提交状态 |
| DEL-01/02 | `TestFilePlanStoreCommitKnownFailuresAndRetry`、`TestFilePlanStoreTombstoneGenerationAndSequenceFloor` | 删除不复活，重建不继承旧事件，floor 不退 |
| REC-01 | 既有 `TestFilePlanStoreMigratesLegacyAppendOnlyLog`；新增 `TestFilePlanStoreLegacySnapshotUpdatePreservesEventsOnReopen`、`TestFilePlanStoreLegacyEventOnlySnapshot`、`TestFilePlanStoreLegacyUnsequencedEventsPreservePositiveCursors` | 旧格式迁移/混合恢复及重复重开不丢事件；3 格式 × 4 种零/正 Seq 组合保留顺序与 cursor，追加后可重开 |
| REC-02/03 | `TestFilePlanStoreV2AuthorityOverridesLegacyMarkers`、`TestFilePlanStoreRepairsLogTailBeforeNextAppend`、`TestFilePlanStoreRejectsCorruptRecordsWithoutFallback` | v2 权威、尾部修复、中间坏记录拒绝 |
| REC-04 | `TestFilePlanStoreMigrationFailureReopensWithoutLoss`、`TestFilePlanStoreProcessDeathDuringMigration` | 迁移故障或中止后可重复恢复，无重复事件 |
| WIN-01 | `TestFilePlanStoreWindowsOccupiedSnapshotPreservesOldState`、`TestFilePlanStoreWindowsReadonlySnapshotFailure`、`TestFilePlanStoreWindowsCaseAndInvalidIdentities` | 占用/只读失败保护旧状态；拒绝大小写碰撞及非法身份 |
| WIN-02 | `TestFilePlanStoreMigratesLegacyAppendOnlyLog` | Windows 实机运行通过，关闭输入句柄后可替换并重开 |
| INT-01 | [persistence_boundary_test.go](../research-assistant/backend/internal/scheduler/persistence_boundary_test.go) 的 6 个 `TestPersistence*` | 失败不发布/计进展/派发；排空异步日志；终态可补偿且并发不偷活跃 claim |
| INT-02 | [plan_store_errors_test.go](../research-assistant/backend/internal/api/plan_store_errors_test.go) 的 5 个 `TestPlanStore*` | 503/404 区分、封存闸门、保留类型化错误与创建幂等键 |
| RACE-01 | `TestPlanStoreConcurrentReturnedCopies`；store/scheduler/api/metrics/usage/audit 六包 race | 各 goroutine 使用各自返回副本，未报告数据竞争 |

真实子进程终止覆盖 7 个边界：快照替换前、替换后（内存发布/响应前，Unix 后续目录 Sync 前）；事件半条写入、事件 Sync 后；迁移首个计划替换后、新日志替换前、替换后。父进程等待子进程到达受控边界后终止并重开，分别检查允许的旧/新状态或尾部修复。辅助入口 `TestFilePlanStoreCrashChild` 在普通主进程跳过，不代表这些父进程用例未执行。这些进程终止用例不等于断电后目录项持久性测试。

REC-04 没有专项注入 gzip 归档压缩失败；本轮的故障与 kill 测试也不代表机器断电或磁盘控制器缓存测试。

## 4. 实际命令、计数与测试边界

环境：Windows/amd64 本机与 Docker Desktop Linux 容器 `golang:1.27.3-bookworm`，Go 1.27.3。命令在对应 backend 根目录运行；Windows 使用工作区内可写 GOCACHE/GOTMPDIR。Linux QA 容器为 `plan-store-fix-qa-20261119`，源码位于 `/work/backend`。

| 平台与检查 | 实际命令 | 结果与本地日志 |
|---|---|---|
| Windows store | `go test -json -count=1 ./internal/store` | 发布审查补充回归后通过；42 个顶层通过、1 个辅助入口跳过；143 条 pass 事件，见 `windows-store.log/.summary.json` |
| Windows store vet | `go vet ./internal/store` | exit 0；`windows-store-vet.log/.summary.json` |
| Windows API | `go test -count=1 -v ./internal/api` | 188 个顶层通过；`windows-api-full.log` |
| Windows 调用链定向 | API 创建/幂等、`TestPlanStore*` 与 scheduler `TestPersistence*` 等相关测试 | 32 个顶层通过；`windows-callers-targeted.log` |
| Windows 调用链 vet | `go vet ./internal/api ./internal/scheduler` | 通过；`windows-callers-vet.log` |
| Linux 全后端 | `go test -json -count=1 ./...` | exit 0；963 个顶层通过、19 跳过，1254 条 pass 事件；45 个含测试包通过；`linux-full-tests.log/.summary.json` |
| Linux race | `go test -race -json -count=1 ./internal/store ./internal/scheduler ./internal/api ./internal/metrics ./internal/usage ./internal/audit` | exit 0；410 个顶层通过、2 跳过，541 条 pass 事件；`linux-race.log/.summary.json` |
| Linux 全后端 vet | `go vet ./...` | exit 0；`linux-vet.log/.summary.json` |
| Linux 后端构建 | `go build -trimpath -o /work/api.fixed ./cmd/api` | exit 0；`linux-build.log/.summary.json` |

计数以 Go JSON 中带 Test 的 pass/skip 记录为准，pass 事件包含父项与子用例，不能当作互不重叠的测试数量。Linux full/race 还包含 QA 中临时加入的 `TestHistoricalNestedReadIsolation`、`TestHistoricalFailedUpdateIsolation`（共 2 个顶层、4 条 pass 事件）；这些临时探针保存在本地 reports，仓库有对应永久回归。Windows store 没有这两项临时探针。

历史缺陷对照：同样的临时探针在未修复基线执行，嵌套读取污染及失败更新泄漏均按预期失败（`baseline-regressions.log/.summary.json`），新版通过。此处预期失败用于证明回归有效，不计为修复后新增失败。

补齐 ISO-05 后的一次全量复跑中，`TestLibrarianLiteratureSearchImportsPDFAndRecordsMetadataOnly` 在 `missing_requirements` 断言失败（1253 条 pass、19 skip、1 fail），原始记录已另存 `linux-full-tests-transient.log/.summary.json`。该既有测试未指定 sources，默认 OpenAlex/GitHub 仍访问公网，Crossref fixture 格式与搜索解析不匹配，arXiv PDF 导入也可能出网；相关 agent/literature 源码与基线相同，调用链不经过 PlanStore。当次日志不足以确定具体是超时、限流还是返回内容变化。本轮未修改该测试或文献实现：基线与新版随后分别定向复跑均通过（`linux-literature-baseline`、`linux-literature-candidate` 的日志与 summary），再按原命令重跑全后端通过，即上表最终结果。该测试的外网隔离缺口仍保留，不将后续通过写成首次复跑无失败。

Linux 全量的 19 个跳过为：5 项真实 provider 上下文、2 项真实科研流程、1 项需要 retrieval Python 环境、6 项缺少 pdftotext 的 PDF 集成、3 项要求只读 mount 的锁/记忆/wiki fixture、1 项未配置 archive fixture、1 项子进程 helper。race 的 2 个跳过为 archive fixture 与 helper。未把这些项目计为通过。

Windows scheduler 全包有 161 个顶层通过、5 个失败，见 `windows-scheduler-full.log`。同环境在原基线和修复工作区逐项复跑，失败名称一致：

| 测试 | 本机失败表现 |
|---|---|
| `TestCloneCachedGitRepositoryUsesCommittedTree` | `git init` 子进程退出 `0xc0000142` |
| `TestRepoPrepareRepositoryRevisionFromUploadedAutoResearchSpec` | repository revision 为空 |
| `TestCloneRepositoryWithRetryChecksOutPinnedCommit` | `git init` 子进程退出 `0xc0000142` |
| `TestExtractRepositoryArchive` | 创建符号链接权限不足 |
| `TestMaterializeUploadedFilesRejectsResearchAssistantSymlink` | 创建符号链接权限不足 |

对照证据为 `windows-baseline-environment.log/.summary.json` 和 `windows-candidate-environment.log/.summary.json`，两者均为上述 5 项失败。本轮没有放宽旧测试断言。初始交接中的 4 项 Windows 历史失败来自先前环境，本轮以实际的 5 项对照为准。

[CI](../.github/workflows/ci.yml) 已新增 Windows store test/vet gate，并保留原 Linux race 范围。原始本地验收没有重跑前端、desktop、docker-sandbox 全量、浏览器 E2E、真实付费模型任务、长期浸泡或性能基准；提交后的远程 CI 状态见关联 PR。

### PR 提交前集成复核

发布前已整合 `origin/master` 的 `12a79ca`（记忆改进及安全/PDF 修复），保留镜像溯源导出、冻结 image digest 与现有安全门禁，再叠加本轮变更。新增零 Seq 兼容修复后，使用独立目录 `/work/pr-integrated` 的实际跟踪源码复跑，没有加入临时 `TestHistorical*` 探针：

| 检查 | 结果 | 证据 |
|---|---|---|
| Linux 全后端 `go test -json -count=1 ./...` | 1000 个顶层通过、19 跳过；1353 条 pass 事件，46 个含测试包通过 | `pr-linux-full.log/.summary.json` |
| 同上六包 `go test -race -json -count=1` | 416 个顶层通过、2 跳过；560 条 pass 事件 | `pr-linux-race.log/.summary.json` |
| Linux `go vet ./...` | exit 0 | `pr-linux-vet.log/.summary.json` |
| Windows store 全包与 vet | 42 个顶层通过、1 helper 跳过；143 条 pass 事件，vet exit 0 | `windows-store`、`windows-store-vet` 的日志与 summary |
| 格式与合并冲突 | 全部 730 个跟踪 Go 文件 gofmt 检查通过；无残留冲突，diff check 通过 | 本机提交前检查 |

首次打包仅包含 backend，缺少兄弟目录中的 claim-evidence/benchmark 夹具，导致两项读取 fixture 失败；补齐仓库已跟踪夹具后，原样全量复跑得到上表结果。缺夹具轮次保留为 `pr-linux-full-missing-fixtures` 日志，不修改业务代码或放宽断言。主分支已为前述文献测试指定 sources，本次集成保留该上游修正。

[PR #8](https://github.com/zzp1221/Research-Assistant-Agent/pull/8) 首轮远程 race 揭示既有 `TestPlanRuntimeExecutesStoredGraphAndReplaysEvents` 只等计划 completed 就立即断言终态事件已写入，撞上计划与事件两次提交之间的窗口。测试现改为执行前订阅事件，以有超时的 channel select 等待已持久化后发布的 `plan_completed`，再执行原计划字段、HTTP 事件和 SSE replay/ID 全部断言；没有增加 sleep 或放宽断言。定向 `go test -race ./internal/api -run '^TestPlanRuntimeExecutesStoredGraphAndReplaysEvents$' -count=20 -json` 20/20 通过，无 DATA RACE，日志为 `pr-api-terminal-race.log`。首轮远程失败日志保留为 `pr-ci-push-linux-go.log`、`pr-ci-pr-linux-go.log`，最终远程检查状态见 PR。

## 5. 现有数据对照

先停止现有后端并备份完整 `/app/data` 和旧 `/app/api`，随后在独立副本对比基线与修复版本。未用迁移探针直接操作唯一原始数据。

- 两个版本均读取到 502 个计划、6865 个业务事件；各自连续两次重开输出一致。
- 规范化输出有 36 处差异：`dependencies`、`required_artifacts` 各 18 处。基线 clone 把原磁盘 `[]` 变为 `null`，修复版本保留 `[]`；已核对原快照。除此之外输出内容相同。
- 基线输出 SHA-256：`342234d34299f8afe47b8e5a00f00fdb970f2c8fc431d98fcf52abe19595d0ac`。
- 修复输出 SHA-256：`869b69ef05e78578c038842b72af81b40e45916c3d47acbccc37f28105759b96`。

探针与汇总为本地 `corpus_probe.go`、`corpus-comparison.summary.json`；原始 `corpus-baseline.json`、`corpus-candidate.json` 含计划数据，仅保存在本地，不随报告发布。此对照验证已有语料的可读性与重开稳定性，不代表所有可能的损坏输入均已覆盖。

## 6. Docker 交付与运行验收

已启动 Docker Desktop，并更新现有容器 `research-assistant-backend-1`：

| 内容 | 容器位置 | 核验 |
|---|---|---|
| backend 源码 | `/app/backend-source` | 与工作区清单一致；544 个 Go/go.mod/go.sum 文件 |
| 新 Linux 可执行文件 | `/app/api` | 与本地 `api.fixed` SHA-256 一致 |
| 源码哈希清单 | `/app/source.sha256` | 容器内 `sha256sum -c --quiet` exit 0 |

二进制 SHA-256：`b08c8f70fb7db3b6723f48c4018027a6182d61e8fc5727e8a168f73bb73398c1`。

实际 backend 主机映射为 `http://127.0.0.1:18080`，前端为 `http://127.0.0.1:15173`。Compose 后端、前端、sandbox gateway/executor、intent 和 optimizer 已启动。当前机器 8080 端口属于其他服务，验收使用 `.env` 的 `BACKEND_HOST_PORT`，不以默认 8080 的响应判断本项目状态。

认证 HTTP smoke 使用人工构造的 require-review 计划，不调用模型或执行科研代码，验证：

1. `/api/me` 与计划创建成功，原图含嵌套 `inputs.metrics.score`。
2. 修改标题后读取计划及事件，第一次重启后标题、嵌套数据、事件列表一致。
3. 删除成功，读取返回 404；第二次重启后仍为 404，计划列表接口正常。
4. 临时计划已删除，仅留下删除协议要求的 tombstone。

证据为 `docker_smoke.py`、`docker-smoke.summary.json`。最终补充检查 `docker-final-check.summary.json` 记录 `/api/health`、认证 `/api/me`、认证 `/api/plans` 均为 200，容器 running，源码清单与二进制哈希匹配。Docker 的 `RestartCount` 不计手工 restart，两个重启以 smoke 记录为准。

启动期间曾遇到原容器异常退出遗留的 memory/wiki PID 1 锁：确认数据卷无活跃 backend 写入者、完成全量备份后，将两个 `.lock` 改名为 `.lock.stale-20261119` 保存。未删业务数据，未修改锁模块；后续启动及两次正常重启均成功。

本次使用 cp 更新现有容器，**未重建 Docker 镜像**。本节哈希与 smoke 对应 PR 提交前的原始交付产物；发布审查补充的零 Seq 兼容修复及之后整合的主分支变更，另由 PR 集成测试验证，不能将本节旧产物视为最终 PR 构建。删除并重建容器会重新使用原镜像，需从当前源码重建镜像或再次交付本次产物。该限制与普通 `docker restart` 不同，后者已验收。

## 7. 备份、回退与剩余边界

本地证据根目录为 `reports/plan-store-fix-2026-11-19/`，包含 `pre-upgrade-data/` 完整停机数据备份、`api.previous`、`api.fixed`、日志与 summary。`reports/` 被 Git 忽略；报告中的文件名用于本机复核，克隆仓库不会自动获得这些原始证据。

旧程序不理解 v2 envelope/tombstone。需要回退时必须停止后端和全部数据写入者，先另备当前数据，再配套恢复 `api.previous` 与 `pre-upgrade-data/` 的完整一致备份。不能只替换二进制后让旧版本打开已经升级的数据。恢复升级前备份也会丢弃备份之后的新变化，需在回退时明确接受该时间点，或先实现并验证离线转换。

本轮仍限于单个存储写进程，没有跨进程文件锁；不承诺多副本写入、网络文件系统或机器断电持久性。删除是逻辑删除；普通事件重试不保证恰好一次；计划、事件与外部副作用不构成整体事务。Windows 原有 5 项失败、Linux 跳过项目、HTTP health 未含 plan-store 项及未测试的归档压缩失败均保留在本报告中。

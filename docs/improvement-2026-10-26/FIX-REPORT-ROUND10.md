# FIX-REPORT-ROUND10：N-01/N-02/N-03/N-05 独立复核与修复

- 日期：2026-10-30（Asia/Shanghai）
- 依据：`docs/design/n01-n03-plan-library-reproducibility-cost.md`、`docs/design/n05-plan-review-hitl.md`
- 基线：`master @ fb8938c` + 第八/九轮工作区实现
- 环境：Docker Compose；Go 测试在 `golang:1.26.1-bookworm`；前端 Node 22；真实 OpenAI-compatible provider

## 1. 结论

两份设计文档中的主体功能已实现：N-01 计划库、N-02 可复现导出包 v2、N-03 成本/预算控制台、N-05 revision/review/question/inbox/diff 均有对应后端 API、存储、前端入口和测试。

本轮独立复核发现并修复了 12 类缺陷（其中 2 个高严重度：审阅门可被草稿绕过、普通执行不绑定最新 revision），并补充回归测试。修复后：

- 后端 `go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部通过；
- 前端 29 个测试文件 / 127 个测试通过，`tsc -b` 与 `vite build` 通过；
- 在最终重建的后端镜像上，六套专项 E2E（plan-library / repro-bundle / plan-revisions / review-hitl / budget-recovery / cost-budget）与 http-smoke 全部通过；
- 关键修复均有 Go 单测或真实 API 复核证据。

## 2. 发现并修复的问题

| # | 严重度 | 问题 | 修复 |
|---|---|---|---|
| 1 | 高 | **Review Gate 可被 revision 编辑绕过**：`POST /plans/:id/revisions` 信任客户端 graph，客户端省略 `review` 字段即可把 `Review.Required` 清空，随后 `revisions/:n/execute` 直接执行未审批计划 | `createRevision` 由服务端保留 review/approval 门禁；新草稿一律回到 `pending`；`executeRevision` 增加 `awaiting_review` 与逐 revision 审批检查；新增 `TestRevisionEditCannotClearReviewGate` |
| 2 | 高 | **普通执行不绑定最新 revision**：保存草稿后点“运行”走 `POST /plans/:id/execute`，节点已是草稿图但 `CurrentRevision` 仍指旧 revision，artifact/事件 revision 错误 | `executePlan` 启动前调用 `bindLatestRevision`，同步 `CurrentRevision` 与节点 `Revision`；新增 `TestPlanExecutionBindsLatestRevision` |
| 3 | 中 | **revision 状态永远停在 draft/approved**：`executing/completed/superseded` 常量存在但无状态迁移，审阅面板看不到“执行中/已完成” | `listRevisions`/`getRevision` 按 `CurrentRevision` + 计划状态做只读投影（executing/completed）；不改变 append-only 存储 |
| 4 | 中 | **`manifest.json.revision` 是时间戳字符串**（`"2026-10-29T..."`），不是设计要求的 revision 数字 | 改为 `int`，取 `plan.CurrentRevision`；新增 `TestExportManifestRevisionIsNumeric` |
| 5 | 中 | **`Enabled=false` 节点仍会被执行**：审阅面板提供“启用节点”开关，但 scheduler 从不读取 `IsEnabled()` | `promotePendingNodesToReady` 将禁用节点标记 `skipped` 并发 `task_skipped`；`findReadyNodes` 过滤禁用节点；依赖跳过节点视为满足；新增 scheduler 单测 |
| 6 | 中 | **导出 artifact 内容未脱敏**：plan/README/events 做了脱敏，但 text/log/report artifact 原样打包，可能泄露 token | 文本类 artifact 打包前 `redactString`，hash 按脱敏后字节计算；新增 `TestExportBundleRedactsTextArtifactContent` |
| 7 | 中 | **双门禁审批顺序错误**：同时存在 runtime approval 与 review 时，`POST /approve` 先批 review 并置 Pending，跳过 runtime approval，scheduler 随后拒绝执行，计划卡住 | `approvePlan` 先处理 `awaiting_approval`，再进入 review；review 分支仅在 runtime approval 已通过时置 Pending；新增 `TestApprovePlanOrdersRuntimeApprovalBeforeReview` |
| 8 | 中 | **复制计划丢失审阅门禁**：复制一个 `require_review` 计划后副本可直接运行 | `duplicatePlan` 复制 `Review.Required` 并置 pending/awaiting_review；新增 `TestDuplicatePlanKeepsReviewGate` |
| 9 | 低 | **单节点执行绕过预算与 usage**：`Scheduler.ExecuteTask` 不检查 plan/user budget，不挂 usage recorder、不结算台账 | `ExecuteTask` 增加 budget gate、reservation、usage recorder、settle/post-check；新增两条单任务预算测试 |
| 10 | 低 | **post-task 预算检查早于 usage 入账**：任务完成后先检查再 `markTaskCompleted`，最后一笔花费永远不会触发 80%/100% 告警 | `scheduler_dispatch`/`scheduler_execute` 把 `postTaskBudgetCheck` 移到 `markTaskCompleted/Failed` 之后；已完成计划超预算只记录 alert 不置 paused |
| 11 | 低 | **100% 预算告警条件不可能成立**、**收件箱 pending 数量被 limit 截断**、**收件箱 cursor 忽略 ID tie-break** | 分别修正条件、分页前统计、`(requested_at,id)` keyset 过滤 |
| 12 | 低 | **审阅面板保存草稿后仍选中旧 revision、节点输入 JSON 切换节点不刷新** | 保存后选中新 revision；textarea 按节点 key 重挂载 |

## 3. 设计文档中的未完成/偏差（如实记录）

| 项 | 状态 |
|---|---|
| N-02 动态 replay（干净容器构建 + 重跑 + 指标对比） | 未实现；`POST /reproduce/verify` 明确 `static_only=true`，只做文件/哈希/checksum 静态校验。设计 5.2.4 允许第一版只做静态校验，但不满足验收标准 N-02#2 的完整重跑 |
| N-02 ZIP 流式写 | 未完全流式：`buildPlanBundle` 会先把全部 artifact 读入内存，再写 zip；有 16MB/128MB 上限，但大包内存占用仍高 |
| N-03 `MaxDownloadBytes` | 仅作为 `RunBudget` 字段与 RuntimePolicy 透传，plan budget 层未计量/未强制执行（TaskUsage 无下载字节字段） |
| N-03 GPU 分钟/存储成本 | 未折算美元（无单价来源），报告已列为已知边界 |
| N-05 增/删节点、增/删边 | 未实现；审阅面板只支持字段/依赖/启用编辑，不支持新增或删除节点 |
| N-05 多选提问 | `PlanQuestion.MultiSelect` 字段存在，但 `answer_option` 只能单值，前端 checkbox 也只保存一个选项，实际不支持多选 |
| N-05 中风险（下载/安装类）触发审阅 | `reviewRiskMediumTasks` 不单独触发 review；只有高风险、显式 require_review、成本阈值、缺需求、L4 触发 |
| N-05 审阅面板“校验” | 校验的是已保存 revision，不是当前未保存表单；表单脏数据需先保存草稿才会被校验 |
| N-01 只读分享链接 | 未实现（设计标注可选/后续） |
| 前端 lint | `npm run lint` 仍有 4 个存量 React Compiler 报错（`useResearchAssistantChatFlow` 3 处、`ModelSettingsPanel` 1 处），均为原有代码；本轮改动的 `PlanReviewPanel` 单独 lint 通过 |

## 4. 验证证据

### 4.1 后端

```bash
cd research-assistant/backend
go build ./...
go vet ./...
go test -count=1 ./...
```

结果：全部通过（含本轮新增 `internal/api`、`internal/scheduler` 回归用例）。

`go test -race -count=1 ./internal/api ./internal/scheduler ./internal/revision ./internal/budget` 也全部通过。

### 4.2 前端

```bash
cd research-assistant/frontend
npm test          # 29 个测试文件 / 127 个测试通过
node node_modules/typescript/bin/tsc -b
node node_modules/vite/bin/vite.js build
```

`tsc -b` 与 `vite build` 通过；`PlanReviewPanel.tsx` eslint 通过。

> 说明：本机 bash 中 `npm run build` 因 npm script shell 找不到 `tsc` 返回 127（环境问题），直接执行上述 node 入口可复现通过。

### 4.3 E2E（Compose 栈）

在最终重建的后端镜像（含本轮全部修复）上：`http-smoke`、`plan-library`、`repro-bundle`、`plan-revisions`、`review-hitl`、`budget-recovery`、`cost-budget` 全部通过。

### 4.4 关键真实 API 复核

- `require_review` 计划编辑 r2 后仍为 `awaiting_review`，未审批执行返回 409 `review_pending`；
- 保存 r2 草稿后 `POST /plans/:id/execute`，`current_revision=2`、节点 `revision=2`、事件 `revision=2`；
- 批准 r2 后执行 r1 返回 409（逐 revision 审批）；
- 导出 zip 的 `manifest.json.revision` 为数字 `2`，`sha256sum -c` 全部通过；
- revision 列表在运行中显示 `executing`，完成后投影为 `completed`。

## 5. 清理

- 删除本轮临时构建目录 `research-assistant/.tmp-build`、工作区 `.tmp-build`、`.tmp-export-test.zip`、`.tmp-export-inspect`；
- 未删除 Compose 数据卷、用户计划、`explore/`、`style-explorer.html`、`shots/`（`2026-10-29-budget-hitl-workbench.md` 明确保留为交付证据）。

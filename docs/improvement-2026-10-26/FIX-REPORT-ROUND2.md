# 剩余问题修复报告（R-01 ~ R-17，2026-10-26 第二轮）

> 依据：`docs/improvement-2026-10-26/02-remaining-issues.md`
> 原则：以实际代码与容器实测为准；文档与实际不符处以实际为准（见第 18 节）。
> 测试环境：Docker Compose 栈（backend / frontend / docker-sandbox gateway+executor / intent-recognition / research-optimizer），真实 LLM `deepseek-v4.1-flash-1017`；所有功能测试在容器内执行。

## 1. 结论

R-01 ~ R-17 已按验收标准完成可实现部分：

- 沙箱默认**无公网出口**（内部离线网络），仅在已批准的安装/联网执行时临时接入 bridge，结束后立即断开；
- API 具备 intent 长度、限流、幂等、并发配额、健康信息脱敏；计划可暂停/恢复/从节点重跑；
- 已接入 Prometheus 指标、LLM token/费用账本、计划停滞看门狗、审计日志与 OpenAPI/CLI；
- 检索升级为真 BM25 + 两阶段 Librarian + 扫描件 OCR；文献检索接入 arXiv/OpenAlex/Crossref/GitHub 与 DOI/arXiv 导入；
- 上传 SHA-256 去重、配额、可选 ClamAV、删除、计划导出（md/json/zip）；
- 前端审批语义、自动运行开关、SSE 断线重连、结构化错误、日志窗口化与移动端节点入口完成；
- 元数据 fixture（Attention 8 作者/2017、Louvain 4 作者/2008）通过，并可用 provider 元数据校正。

明确未做（非目标/持续项）：协作/角色/完整多租户 RBAC、计划自由编辑、定时任务、gVisor 强制默认、分模块 70% 覆盖率阈值。

## 2. 测试方法

| 层 | 命令/方式 | 结果 |
|---|---|---|
| 后端构建/静态 | 容器内 `go build ./...` / `go vet ./...` | 通过 |
| 后端单测 | 容器内 `go test ./...` | 全部包通过 |
| 后端竞态 | 容器内 `go test -race ./internal/scheduler ./internal/api ./internal/store ./internal/metrics ./internal/usage ./internal/literature` | 通过 |
| 覆盖率 | 容器内 `go test -coverprofile` | 59.9%（上一轮 58.1%；CI 门槛 50%） |
| 沙箱服务 | 容器内 `go build` / `go vet` / `go test ./...` | 通过 |
| 前端单测 | `node:22-bookworm-slim` 容器内 `npm ci && npm test` | 22 文件 / 99 测试通过 |
| 前端构建/lint | 同容器 `npm run build` / `npm run lint` | 通过 |
| HTTP smoke | 容器内 `test/e2e/http-smoke.mjs` | 17/17 通过 |
| 沙箱网络 | 容器内 `test/e2e/sandbox-network.mjs` | 8/8 通过 |
| 真实 LLM 绘图 | 容器内 `test/e2e/plan-run.mjs --execute --full-access` | completed，7 节点，2 图像 artifact 同 blob |
| OCR | 容器内上传扫描版 PDF | `extractor=tesseract-ocr`，1 页 82 字符，文本正确 |
| 文献 | 容器内真实 arXiv 搜索/导入 | 2 条结果；导入 Attention 8 作者/2017/15 页 |
| CLI | 容器内 `scripts/ra.mjs` openapi/create/status/export/list/health | 通过 |
| UI 冒烟 | Playwright 容器访问 `http://host.docker.internal:15173` | 通过（无假刊头、0 console error、自动运行开关可见） |

## 3. R-01 沙箱网络与硬限制

**实现**
- 默认网络不再使用 `--network none`：Docker 不允许把 none 容器再接入 bridge，因此改为内部网络 `research-assistant-offline`（`--internal`、无网关、`enable_icc=false`，见 `docker_security.go` / `ensureOfflineNetwork`）。
- `POST /api/v1/sandboxes/:id/network` 打开/关闭默认 bridge；后端在安装依赖和已批准联网执行前后成对调用（`openSandboxNetwork`），失败即写 `MissingRequirement(network_control_failed)`。
- `runtimeActionForTask` 为 paper_code_execute / benchmark / autoresearch / experiment 等执行节点创建 `network_access` 审批；同一计划的首次网络批准会复用到后续网络节点。
- RuntimePolicy 限制进入执行路径：镜像预检（`MaxImageBytes`）、`docker stats` 网络字节（`MaxDownloadBytes`）、`du` 磁盘（`MaxDiskGB`）、执行超时（`MaxRuntimeSec`）；超限返回 507 并带用量。
- `EnvironmentManifest` 记录 `network_mode`、接收/发送字节与 hard limits。
- `capabilities` 暴露 `network_mode/network_allowed/security_profile`。

**证据**
- 容器 E2E：默认解析 pypi.org 返回 `BLOCKED gaierror`；打开 bridge 后 `RESOLVED 151.101.128.223`；关闭后再次 `BLOCKED`；usage 返回 limits 与网络字节；删除成功。
- 真实绘图计划：pip 安装 numpy/matplotlib 成功（安装阶段临时联网），随后代码执行与绘图完成。

## 4. R-02 前端审批与文案

- runtime action 标题按计划类型生成：`安装运行依赖/拉取运行时镜像`（非论文）与 `安装论文依赖/拉取论文运行时镜像`（论文）。
- 计划审批与运行时审批继续分离；pending runtime action 时不显示计划级“审批并运行”（已有组件测试）。
- blocked/canceled/skipped/ready 文案与颜色完整；假刊头已不存在；colophon 展示真实规划来源/权限/触发方式。
- 证据：`GraphPanelRuntimeAction`、`GraphPanelRunTrigger`、`GraphPanelMissingRequirements` 测试；UI 冒烟无“第 12 卷”等假元数据。

## 5. R-03 自动运行

- 新增持久化开关“创建后自动运行执行图”（默认关闭）。
- 仅当开关开启 + Code_Execution + 无计划审批 + 存在 render_plot 时自动启动；执行请求带 `{"trigger":"auto"}`。
- 消息顺序固定：`执行图已生成。` → `已生成执行图并自动启动；可在执行台取消。` → 运行时消息；需要审批时不出现自动启动文案。
- 计划详情显示 手动运行/自动运行/未运行。
- 测试：`AutoRunOnCreate`（4）、`ExecutePlanTrigger`（2）、`GraphPanelRunTrigger`（5）。

## 6. R-04 API 健壮性

- `POST /api/plan`：intent ≤ 20000 字符（`intent_too_long`）、附件 ≤ 8、per-user token bucket（429 + Retry-After）、`Idempotency-Key` 24h（只缓存 2xx，重放标记 `Idempotency-Replayed`）、per-user 活跃计划上限（429 `active_plan_quota_exceeded`）。
- `/api/health` 只返回 ok/version/依赖状态；内部服务 URL 默认隐藏（`HEALTH_VERBOSE=true` 才输出），provider 详情从不返回。
- CORS 允许 `Idempotency-Key`，暴露 `Retry-After/Idempotency-Replayed/Content-Disposition`。
- 证据：`hardening_test.go`、`security_regression_test.go`、`cors_test.go`、HTTP smoke（30000 字符 → 400，幂等重放同 plan id）。

## 7. R-05 可观测性与成本

- 无第三方依赖的 `internal/metrics`：HTTP、计划/任务、LLM calls/tokens/latency、沙箱执行、active_plans、plan_stalled；`GET /api/metrics` 与 `GET /metrics`（Bearer 鉴权）。
- LLM 账本：
  - 直连 Generate 通过 `llmclient.RecordUsage` 记录；
  - Eino ChatModel 节点通过 `usageChatModel` 包装（Generate/Stream 均记录）；provider usage 缺失时按字符估算并标记 estimated。
  - 计划/节点保存 prompt/completion tokens、calls、cost（`LLM_PRICE_*_PER_1K_USD`）、sandbox seconds、duration。
- 看门狗：无活动超过 `PLAN_STALL_SECONDS` 发布 `plan_stalled` 并计数。
- 证据：绘图计划 `f026a74d-9cd5-4d63-b2c9-ac902114f872` → `llm_calls=2 tokens=3689/3626 sandbox_seconds=11.69`；`/api/metrics` 可见 `llm_calls_total`、`llm_tokens_total`、HTTP/计划指标。

## 8. R-06 检索

- 真 BM25：IDF、k1=1.2、b=0.75、长度归一化；保留中文 bigram；`SearchOptions{TopK,Section,Page,SectionBoost}`，旧 `Search` 兼容。
- Librarian 两阶段：Outline → 选 3~6 章节 → 章节内 BM25 → 合并并附 page/section 标签与 `[retrieval] sections=... chunks=...` 轨迹；24k 字符预算。
- OCR：pdftotext 无文本时尝试 pdftoppm+tesseract（`PDF_OCR_MAX_PAGES` / `PDF_OCR_TIMEOUT_SECONDS`），不可用时返回明确错误；backend 镜像已安装 `tesseract-ocr`（eng/chi_sim）。
- 引用定位：`LocateQuote` 忽略大小写/空白/断行连字符，返回页码与原文偏移。
- 证据：`bm25_test.go`、`quote_test.go`、`pdf_ocr_test.go`、`librarian_test.go`；扫描 PDF 上传结果 `status=extracted extractor=tesseract-ocr chars=82`。

## 9. R-07 文献检索与导入

- `internal/literature`：arXiv Atom、OpenAlex、Crossref、GitHub（可选 token），缓存、按 host 限流、超时、结构化错误；| 去重按 DOI/arXiv ID/标题。
- `GET /api/literature/search`、`POST /api/literature/import`；导入 PDF 有大小上限与 `%PDF` 校验，PDF 按 URL 缓存。
- 计划意图含 arXiv URL/ID/DOI 时自动导入（预算 `LITERATURE_AUTO_IMPORT_TIMEOUT_SECONDS` 默认 75s，失败静默降级），provider 元数据用于校正 PDF 元数据。
- 证据：真实搜索返回 arXiv 结果；真实导入 `1706.03762` → 标题/8 作者/2017/15 页，落盘为 upload；`literature_test.go` + `literature_api_test.go`。

## 10. R-08 数据生命周期

- 上传 SHA-256 去重（同 owner+hash 秒回同一 upload/document，返回 `deduplicated=true`）；per-user 配额（文件数/字节）；`DELETE /api/uploads/:id`；`UPLOAD_TTL_DAYS` 定期清理。
- `DELETE /api/sessions/:id`（有运行中计划时 409）；`DELETE /api/plans/:id`（既有）。
- `GET /api/plans/:id/export?format=md|json|zip`：README、plan.json、events.jsonl、artifact blob 的最小复现包。
- 证据：HTTP smoke 去重/删除/session 删除；`uploads_lifecycle_test.go`；zip 以 `PK` 开头。

## 11. R-09 SSE/HTTP 可靠性

- Store 增加 `AppendEventReturning`；live SSE 事件发布时带上已分配 seq，并写 `id:` 行。
- SSE 支持 `Last-Event-ID` / `after_seq` 增量；前端指数退避（1/2/4/8/16s，上限 30s）重连、每次刷新 ticket、按 lastSeq 续传、终态停止、去重。
- 创建计划带 Idempotency-Key 与 180s 超时提示。
- 证据：`event_seq_test.go`、`plan_runtime_test.go`（id: 1）、`planStreamReconnect.test.ts`（11）、`useResearchAssistantRuntimeReconnect.test.tsx`（2）、`researchAssistantApiReliability.test.ts`（7）；实机 SSE 抓包 6 条 `id:` 且 seq 单调。

## 12. R-10 调度性能

- 主循环由 150–200ms 轮询改为 notify channel 事件驱动 + 1s 兜底；任务完成/失败/暂停/取消立即唤醒。
- `PLAN_MAX_CONCURRENT_TASKS` / `PLAN_GLOBAL_MAX_TASKS` 可配置；全局 semaphore；per-user 活跃计划配额。
- 快照 dirty check（既有）+ 事件驱动减少空转；指标 active_plans/queue/stall。
- 证据：`control_test.go`（暂停不派发、恢复完成、重跑、停滞事件）、`runtime_governance_test.go`、`event_seq_test.go`。

## 13. R-11 测试与 CI

- 新增/扩充测试：api（hardening/security/cors/audit/literature/uploads/export）、scheduler（control/gate/event seq）、agent（network/usage model/prompt summary）、documents（BM25/quote/OCR/metadata/PDF limits）、literature、scan、metrics、usage、store。
- 安全回归：伪造身份、超长 intent、SSRF、上传伪造、限流、SSE ticket、路径遍历（既有）。
- 故障注入：LLM 重试/超时（llmclient）、镜像拉取失败（agent）、审批拒绝（scheduler）、后端重启恢复（store）。
- CI：后端 test/vet/race/安全测试/govulncheck；前端 test/build/lint/CLI 语法；UTF-8 gate；compose smoke（main/dispatch，真实 compose 栈 + HTTP smoke）。
- 覆盖率：59.9%（目标 ≥70% 未达，列持续项）。

## 14. R-12 代码质量与文档

- 移除字符串 context key（`logChannel/codeChannel/containerID` → typed key）。
- 移除 emoji 错误文案；API 结构化 code/error/remediation；前端 `formatApiError`。
- UTF-8/mojibake 检查脚本 + CI；compose 实际为 UTF-8（审计描述过时）。
- 删除未接入的 `internal/tools/ask_user_question` 死代码。
- README（根/产品/沙箱）同步；OpenAPI 3.1。

## 15. R-13 产品能力

- 已实现：`POST /api/plans/:id/pause|resume`、`POST /api/plans/:id/tasks/:taskId/run`（重置目标+下游并重跑）、OpenAPI 3.1、CLI、SSE 通知事件（completed/failed/stalled/paused/resumed）。
- 未实现：计划自由编辑、定时/watch list、协作/分享/角色、完整多租户 RBAC——保持 owner 隔离并在 README/OpenAPI 说明边界。

## 16. R-14 错误与 UX

- 统一错误码 + 中文 title/detail/remediation；GraphPanel 展示 missing requirements 的 kind/detail/修复建议；ExecutionSidebar 对执行失败/流错误显示结构化错误。
- 日志默认窗口 500 行、级别过滤、显示全部、下载完整 .log；10k 行测试仅渲染 500 行。
- 窄屏（≤767px）增加“查看节点”按钮（aria-label=查看节点详情）。
- 证据：`apiError.test.ts`（23）、`logWindow.test.ts`（14）、`ExecutionSidebarLogs/Error`、`GraphPanelMobileNode/MissingRequirements`。

## 17. R-15 / R-16 / R-17

**R-15**
- 默认离线网络；`SANDBOX_SECURITY_PROFILE=strict` 提供非 root + read-only rootfs + tmpfs；`SANDBOX_RUNTIME` + allowlist 支持 runsc。
- 审计 JSONL（`AUDIT_LOG_PATH`）+ `GET /api/audit/export`（调用者隔离；`AUDIT_EXPORT_TOKEN` 可全局导出）。
- CI govulncheck + npm audit（信息性）；Docker socket 边界文档化。
- 未默认启用 strict/gVisor（需要宿主 runtime 且会改变 pip 行为），文档注明。

**R-16**
- Attention fixture：8 作者、2017、1706.03762；Louvain：4 作者不拆分、2008、0803.0476；Apriori 保持；Source/Confidence/Warnings；`ApplyProviderMetadata` 用 arXiv/Crossref 校正。
- 证据：`metadata_test.go`（12 项）全部通过；真实 arXiv 导入返回 8 作者/2017。

**R-17**
- 上传 SHA-256 去重、配额、删除；ClamAV INSTREAM（未配置明确 skipped）；PDF 解析输入上限（`PDF_PARSE_MAX_MB`）与超时（`PDF_PARSE_TIMEOUT_SECONDS`），失败可见；解析在独立 pdftotext 进程。
- 证据：`scan_test.go`、`pdf_limits_test.go`、`uploads_lifecycle_test.go`。

## 18. 与审计文档的差异 / 额外发现

| 文档描述 | 实际与处理 |
|---|---|
| `/api/health` 泄露 provider base_url/model | 上一轮已部分脱敏；本轮统一隐藏内部 URL，只保留 ok/version/依赖状态 |
| 前端假刊头“第 12 卷” | 本轮开始时已不存在；保留 UI 冒烟断言防回归 |
| docker-compose 中文注释乱码 | 实际文件为 UTF-8；新增 UTF-8 gate |
| 20k intent 不校验 | 现已 400；30000 字符实测 400 |
| “network none 后可按需 bridge” | Docker 不允许 none → bridge，改为内部离线网络 + bridge 叠加；见 R-01 |
| 无 token/cost 账本 | 已实现；但 Eino ChatModel 节点原先绕过记录，本轮补 `usageChatModel` |
| verify_result 能校验绘图 | 图像 base64 曾进入 prompt 导致 180s×3 超时；现提示词省略图像，计划可完成，但校验结论为“不可确认图片内容”，这是 LLM 视觉能力边界 |

## 19. 主要变更文件

**后端**：`internal/metrics`、`internal/usage`、`internal/audit`、`internal/scan`、`internal/literature`、`internal/agentctx`、`internal/agent/sandbox_network.go`、`input_summary.go`、`usage_model.go`、`internal/api/{ratelimit,idempotency,observability,errors,plan_control,plan_export,literature,openapi}.*`、`internal/scheduler/{scheduler,runtime_gate,executor}.go`、`internal/store/*`、`internal/documents/{bm25,outline,quote,pdf,metadata,store}.go`、`internal/sandbox/opensandbox.go`、`models/*`、Dockerfile（OCR 工具）。
**沙箱**：`engine/options.go`、`docker.go`、`docker_security.go`、`capabilities.go`、`main.go`。
**前端**：`planStreamReconnect.ts`、`apiError.ts`、`logWindow.ts`、`useResearchAssistantRuntime/ChatFlow/App`、`GraphPanel`、`ExecutionSidebar`、`ChatComposer`、`contracts/api.ts`。
**测试/工具**：`test/e2e/{http-smoke,sandbox-network,plan-run}.mjs`、`frontend/test/e2e/ui-smoke.mjs`、`scripts/{ra.mjs,check-utf8.mjs,container-go.sh}`、CI。

## 20. 残留与建议

1. 计划存储仍为文件 + blob（29MB plans.json 来自历史测试）；生产建议迁移 SQLite/对象存储并增加保留策略。
2. 自动导入在慢网下最多占用 75s，超时后计划不含 PDF；可在前端显示“导入仍在后台进行”。
3. verify_result 无法看到图像像素；如需视觉校验应增加多模态模型或缩略图附件。
4. strict 安全 profile / gVisor 需在宿主安装并调整 pip 流程后默认开启。
5. 覆盖率 59.9%：api/scheduler/agent 仍低于 70%，建议后续按模块补测。
6. R-13 的协作、通知渠道、多租户 RBAC 未实现（明确非目标）。
7. Paper_Reproduction 的计划创建在真实 LLM 下实测约 108s（导入元数据 + agent planner），仍在 180s 前端超时内；慢网下 PDF 自动导入可能降级为仅元数据。

## 21. 最终验证补充（2026-10-26 22:35 CST）

| 场景 | 结果 |
|---|---|
| 沙箱网络 E2E（容器内） | 默认 `BLOCKED gaierror` → 打开 bridge `RESOLVED 151.101.128.223` → 关闭后再次 `BLOCKED`；usage 返回 limits/字节；删除成功 |
| 真实绘图计划（full_access） | `f026a74d-9cd5-4d63-b2c9-ac902114f872` completed，7 节点，2 个 image artifact 指向同一 blob；`llm_calls=2 tokens=3689/3626 sandbox_seconds=11.69` |
| 扫描件 OCR（容器内） | upload `5f839bd1-9a62-4343-8da0-025869a94e71` → `extractor=tesseract-ocr`，1 页 82 字符，文本正确 |
| arXiv 显式导入（容器内） | 29s；`1706.03762` → 8 作者/2017/15 页；upload `e783bdf4-9073-40c5-a3fd-4d8ba005749d` |
| arXiv URL 自动导入计划（容器内） | `acb42504-b1d3-49aa-8ebc-aaba4b91d61b`，108s，14 节点；`context.paper` 8 作者/2017/source=arxiv，attachments=1（PDF 已附） |
| live SSE | 计划 `14053e48-98b1-473d-bd14-45b7b7776019` 抓包 6 条 `id:`，seq 1..N 单调 |
| 审计/指标（容器内） | `/api/audit/export` 返回本人事件；`/api/metrics` 可见 `llm_calls_total`、`llm_tokens_total`、HTTP/计划指标 |
| UI 冒烟（Playwright 容器） | 通过：标题、无假刊头、composer、自动运行开关；截图 `reports/round2/ui-smoke.png` |
| CLI（容器内） | `scripts/ra.mjs` openapi/create/status/export/list/health 全部通过 |
| 最终测试 | 后端 `go vet + go test -count=1 ./... + race`、沙箱 `go vet + go test -count=1 ./...`、前端容器 `npm test + build + lint` 全部通过（exit 0） |

> 说明：Paper_Reproduction 计划创建在真实 LLM 下约 108s（元数据导入 + agent planner），仍低于前端 180s 超时；慢网下 PDF 自动导入可能降级为“仅元数据”，显式 `/api/literature/import` 仍可补齐 PDF。

## 22. 清理记录

- 删除仓库内临时文件：`.dsh-tmp*`（含 OCR/UI/plan 快照/SSE 抓包）、`research-assistant/frontend/dist`、根目录误产生的 `research-assistant;C` 与 `test-results/`、沙箱/API 测试临时脚本。
- 保留可复用测试资产：`test/e2e/{http-smoke,sandbox-network,plan-run}.mjs`、`frontend/test/e2e/ui-smoke.mjs`、`scripts/{ra.mjs,check-utf8.mjs,container-go.sh}`。
- 删除重复镜像 tag（`docker.m.daocloud.io/library/*` 与本地同一镜像），保留 product 镜像与测试基础镜像（golang/node/python 等，Docker Hub 当前不可达，删除会影响重建）。
- 删除 Playwright 镜像（3.38GB，UI 冒烟已完成并保留截图/脚本）；删除未使用测试卷 2.55GB（含 npm/go 缓存）。
- 清理后：`docker system df` Images ~6.0GB（product 镜像 + 测试基础镜像）；无 dangling image；无泄漏 sandbox 容器；compose 栈健康。
- 计划/上传卷中的历史数据未删除（属于运行中项目数据）；本轮测试计划仍在，可通过 `DELETE /api/plans/:id` / `/api/sessions/:id` 按 owner 清理。
- 清理后重新执行了 `go mod download`（`GOPROXY=https://goproxy.cn,direct`）并跑完整后端/前端测试，确保工作区可复现。


## 23. 最终状态

- 前端创建计划超时预算调整为 240s（Paper_Reproduction 含 arXiv 导入与 agent planner 实测 108s）。
- 最终重新构建并启动 backend / frontend / sandbox 镜像；compose 健康检查通过，0 个泄漏 sandbox 容器。
- 工作区无本轮临时文件；`reports/round2/ui-smoke.png` 保留为 UI 证据。

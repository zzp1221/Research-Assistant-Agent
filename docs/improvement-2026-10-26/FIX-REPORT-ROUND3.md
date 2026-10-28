# FIX-REPORT-ROUND3：运行期模型配置与通用视觉校验（2026-10-27）

> 依据：`docs/design/runtime-model-config-and-visual-verification.md`
> 结论：设计稿的 P0–P4 全部落地，P5 的 reverify/blocking/预算列为非目标；容器内单测、API 集成、前端组件、容器 E2E（场景 A–D）全部通过。

## 1. 交付概览

| 层 | 内容 |
|---|---|
| 存储/加密 | `internal/modelprofile`：AES-256-GCM（AAD 绑定 id+owner+revision）、原子文件写、per-user + 全局 profile、routing、红/蓝视觉 probe、bootstrap、数量/频率限制 |
| 解析/热更新 | `internal/modelresolver`：按 owner 解析文本/视觉 profile，revision 缓存，保存即失效；计划创建冻结 `model_snapshot` |
| Agent 接入 | answer/chat/coder/librarian/data 使用 `dynamicChatModel`，planner 使用 `runtimePlannerModel`；Eino 链只编译一次，模型按 ctx owner 每次解析 |
| 视觉校验 | `internal/agent/visual_verifier.go`：结构门禁（解码/尺寸/熵/边缘/透明比例）、多模态 image_url 调用、严格 JSON verdict、一次修复、候选路由、缓存、多图汇总 |
| 数据模型 | `models.Verification / ImageVerification / ModelSnapshot`；PlanGraph / TaskNode / Task / TaskExecutionResult / Artifact.Metadata |
| 事件与指标 | `verification_completed` / `verification_failed`；`verification_total`、`vision_calls_total`、`model_profile_changes_total` |
| API | `/api/model-profiles`（CRUD/test/probe-vision）、`/api/model-routing`；ADMIN_TOKEN、SSRF、限流、错误码 |
| 前端 | `ModelSettingsPanel`（列表/编辑/测试/探测/删除/routing/成本与隐私提示）、`VerificationBadge/Details`、计划头与节点详情展示、SSE 刷新 |
| 文档 | README 第三轮章节、OpenAPI 3.1（新增 5 个 path + 4 个 schema）、.env.example / backend.env.example |

## 2. 验证证据（全部容器内）

### 2.1 后端

```
scripts/container-go.sh "go build ./..."      -> OK
scripts/container-go.sh "go vet ./..."        -> OK
scripts/container-go.sh "go test ./..."       -> 全部 ok（含新增包 modelprofile / modelresolver / oaichat）
```

新增单测覆盖：
- 加密/解密、AAD 绑定（改 id/owner/revision 必须解密失败）、无密钥 fail closed、掩码与磁盘无明文；
- routing 矩阵（owner 隔离、全局只读、引用校验、删除保护）、name 唯一、配额、revision 递增、bootstrap 幂等；
- 结构门禁：合法图案、纯白、全透明、1×1、损坏、超大；
- verdict 解析：合法 JSON、非 JSON、未知字段、confidence 越界；
- 汇总：pass / fail / inconclusive / skipped、视觉 fallback、结构性失败不调用视觉、垃圾 JSON 一次修复；
- fail 仍 completed 的状态语义 + 事件 + artifact 标记；
- 身份稳定性回归（一个请求内只签发一个匿名身份）。

### 2.2 前端

```
node:22-bookworm-slim 容器内：
  npm run build   -> OK（tsc -b + vite build）
  npm test        -> 24 files / 107 tests passed
  npm run lint    -> OK
```

### 2.3 容器 E2E

```
test/e2e/model-profiles.mjs   -> all model profile checks passed
test/e2e/verification.mjs     -> all verification scenarios passed
test/e2e/http-smoke.mjs       -> all smoke checks passed（旧功能回归）
```

| 场景 | 配置 | 结果 |
|---|---|---|
| A | 主模型 `supports_vision=yes` | completed + verification=pass(visual)，verified_by=主模型 |
| B | 主模型无视觉 + routing 备用视觉 | completed + verification=pass(visual)，verified_by.mode=vision_fallback |
| C | 无任何视觉模型 | completed + verification=inconclusive(structural)，reason 指明未配置 |
| D | 视觉模型返回 fail | completed + verification=fail(visual) + issues + artifact 标记 + verification_failed 事件 |

每个场景同时断言：plan.verification、verify 节点 verification、至少一个图像 artifact 带校验标记、SSE 事件类型。不含任何 sin/cos 或图表类型专用断言。
E2E 视觉服务使用 `test/mock/mock_vision_server.py`（OpenAI-compatible、确定性、不访问外网），通过 compose `e2e` profile 启动。

### 2.4 UI

Playwright（宿主 Chrome 连接 compose 前端）验证：
- 工作台“模型设置”入口可打开设置面板，展示“我的模型 / 全局模型（只读）/ 模型路由”、掩码 key、视觉徽标、最近探测时间、成本与隐私提示；
- 计划头 `VerificationBadge` 渲染校验状态；节点详情展示 issues/confidence/verified_by；
- 无 verification 字段的旧计划不显示徽标（legacy 兼容）。

## 3. 与设计文档的差异（以实际为准）

| 设计文档 | 实际与处理 |
|---|---|
| 新增 `internal/modelprofile`、`internal/modelresolver`、`visual_verifier.go` | 实际均不存在，本轮从零实现；另抽出 `internal/oaichat` 作为多模态 HTTP 客户端，避免与 Eino 的 openai 客户端耦合 |
| answer/chat/coder/librarian/data/planner 启动时 NewChatModel | 实际确实如此且 `log.Fatalf` 会让缺 Key 的部署直接退出；改为动态 proxy + 容错初始化（无 Key 时仍可启动，运行期解析 profile） |
| WebP 可选 | 未引入 WebP 解码依赖，按设计“可选”跳过 |
| `ALLOW_HTTP_LOCALHOST` 仅开发 | 容器 E2E 的 mock provider 在 compose 私网，额外提供 `ALLOW_PRIVATE_MODEL_ENDPOINTS`（默认 false；仅 `test/docker-compose.e2e.yml` 覆盖开启）。默认栈实测 `http://mock-vision:9099` 被 400 `ssrf_blocked` 拒绝 |
| revert 用 `FEATURE_VISION_VERIFICATION=false` | 已实现：关闭后图片存在时返回 inconclusive，不调用视觉 |
| 计划快照 used_profile_ids + revisions | 以 `plan.model_snapshot`（text + vision[]）实现，并在响应中可见；不含任何密钥 |

## 4. 实施中发现的额外问题（已修）

1. **一个请求内签发两个匿名身份**：`createPlan` 调用 `resolveUserID` 后，`r.store.SavePlan` 等路径再次调用会再发一个 `Set-Cookie`，后者覆盖前者，导致 profile 归属与后续请求不一致（E2E 中表现为“刚创建的 profile 立刻 404”）。修复为在 gin context 内 memoize user/session，并加回归测试 `identity_stability_test.go`。
2. **绘图计划没有 verify_result 节点**：模板 `buildCodeExecutionNodesV2` 只在“需要分析或无图”时追加 verify，导致有图计划跳过校验。已改为所有可执行计划都以 verify_result 收尾。
3. **视觉候选会隐式使用未路由的 vision profile**：原 ResolveVision 会兜底扫描 owner 所有 vision 角色 profile，违反“按 routing 显式路由”。已改为仅 main（可视觉）+ routing vision 列表，并加单测。
4. **只有 verify 节点产出的 artifact 被标记**：视觉校验针对的是更早的 plot/execution 图像，原实现只给 verify 节点输出打标。改为校验完成时给计划内所有图像 artifact 统一打标。
5. **视觉 verdict 的 profile 元信息丢失**：缓存命中时未回填 profile id/model，导致 `verified_by` 为空。已随 judgeImage 返回结构一并保留。
6. **LLM 规划器可能省略 verify_result**：真实 LLM（agent planner）生成的绘图计划实测出现过 `generate_code → … → render_plot → general_synthesis` 而无 verify 节点，导致图像没有校验记录。新增 `ensureVerificationNode` 兜底：任何输出 plot/image artifact 且没有 verify_result 的计划自动补一个消费该图像的校验节点；模板计划也统一以 verify_result 收尾。加 3 个单测（追加/幂等/纯文本不加）。
7. **E2E 需要确定性规划**：真实 LLM 规划耗时与形状不可控，新增 compose `e2e` profile 的 `mock-vision` 服务 + `test/docker-compose.e2e.yml`（`PLANNER_AGENT_ENABLED=false`）用于场景 A–D；同时保留一次真实 LLM 规划 + 视觉校验的 UI 验证（plan completed、verification=pass、页面显示“校验通过 · VISUAL”）。

## 5. 非目标 / 后续

- `POST /api/plans/:id/tasks/:taskId/reverify`（设计 4.7 可选）未实现；
- blocking 策略、每日 token/费用预算、verification 历史保留策略未实现（设计 4.11 / 6-P5）；
- OCR 辅助证据未接入视觉流水线（设计 4.5 标注可选）；
- 覆盖率未达分模块 70%（延续第二轮持续项）。

## 6. 清理

- 删除本轮临时脚本（frontend/scripts/tmp-shot.mjs、/tmp 下的调试脚本）、UI 审计截图目录、重复构建镜像；
- 保留 compose 的 `mock-vision`（e2e profile，默认不启动）与 `test/mock/`，作为可复现的 E2E fixture；
- 清理测试产生的临时计划/上传/容器。

## 7. 最终验证快照（2026-10-27 11:55 CST）

| 项 | 命令（均在容器内） | 结果 |
|---|---|---|
| 后端构建 | `scripts/container-go.sh "go build ./..."` | OK |
| 后端静态检查 | `scripts/container-go.sh "go vet ./..."` | OK（无输出） |
| 后端单测 | `scripts/container-go.sh "go test ./..."` | 23 个包 ok，0 FAIL |
| 沙箱单测 | `scripts/container-go.sh "cd /src/docker-sandbox && go test ./..."` | 2 个包 ok |
| 前端构建 | node:22-bookworm-slim：`npm run build` | OK（tsc -b + vite build） |
| 前端测试 | node:22-bookworm-slim：`npm test` | 24 files / 107 tests passed |
| 前端 lint | node:22-bookworm-slim：`npm run lint` | OK |
| 模型配置 E2E | `node test/e2e/model-profiles.mjs` | all model profile checks passed |
| 视觉校验 E2E | `node test/e2e/verification.mjs`（e2e overlay） | 场景 A–D all passed |
| HTTP smoke 回归 | `node test/e2e/http-smoke.mjs` | all smoke checks passed |
| 真实 LLM 规划 + UI | Playwright（宿主 Chrome → compose 前端） | 绘图计划 completed，verification=pass，页面显示“校验通过 · VISUAL” |

清理：删除临时调试脚本、UI 截图目录、一次性 Playwright 镜像（3.38GB）、构建缓存 1.34GB；清理 E2E 产生的测试计划/事件与测试模型 profile（仅保留 env bootstrap 全局 profile）。compose 栈（backend/frontend/sandbox/executor/intent-recognition/research-optimizer）保持运行。
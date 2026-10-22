# Research Assistant Agent — Remediation Plan

## Phase 0 — Git 基线

- `git init`
- 提交当前完整快照 `chore: baseline snapshot before remediation`
- 后续每阶段独立提交，便于回滚。

## Phase 1 — P0 安全边界（最高优先级）

### 1.1 移除危险直接执行入口

- 删除 `POST /api/execute` 的旧实现（接受客户端任意 `Inputs`）。
- 新增计划内任务执行接口：`POST /api/plans/:id/tasks/:taskId/execute`
  - 只允许计划 owner；
  - 只允许计划内真实 task ID；
  - 审批未通过的计划拒绝执行；
  - 任务输入只从 PlanGraph/Artifact 读取，不接受客户端任意路径。
- 前端改为调用新接口；节点执行仍保留，但走计划治理。

### 1.2 路径与输入校验

- 新增统一路径校验：
  - `storage_path` 必须位于 `UPLOAD_ROOT`；
  - `workspace_path` 必须位于 `SANDBOX_WORKSPACE_ROOTS`；
  - 拒绝符号链接、`..`、绝对路径越界；
  - 上传目录/工作区权限改为 0700。
- 移除 `task.ID` 直接拼 `/tmp` 的逻辑；由服务端生成 UUID 工作区。
- `POST /api/plan`、`/api/execute` 等 JSON 请求增加 `MaxBytesReader`。

### 1.3 沙箱控制面

- `docker-sandbox` 与 `backend/internal/sandboxserver`：
  - 容器 ID 必须匹配 `^[a-f0-9]{12,64}$`；
  - 创建时打 `research-assistant.sandbox=true` 标签；
  - 执行/删除前 `docker inspect` 校验标签；
  - 拒绝容器名；
  - `DeleteSandbox` 返回真实错误。
- Compose：
  - 移除默认 `local-sandbox-token` / `local-optimizer-token`；
  - 使用 `.env` 随机 token；
  - backend/sandbox 端口绑定 `127.0.0.1`；
  - backend/sandbox 容器增加基础加固（cap_drop、no-new-privileges、pids/mem 限制）。
- 文档明确：Docker socket 仍是高权限边界，生产应使用 socket proxy/rootless。

### 1.4 认证与审批

- `APIAuthMiddleware` 在 `REQUIRE_API_AUTH=true` 时 fail-closed；
- 前端支持 `VITE_API_AUTH_TOKEN`（axios + fetch）；
- SSE 使用短期 stream ticket（避免把长期 token 放 URL）；
- `approvePlan` 只允许 `awaiting_approval` 状态；
- full reproduction 从 PlanGraph 的 task inputs 派生审批要求；
- 增加安全回归测试。

## Phase 2 — P1 调度器与 Artifact 正确性

- `ExecutePlan` 在 ctx 超时/取消时写终态事件并取消运行中任务。
- `markTaskStarted`/`runReadyNodes` 错误统一转为 plan failed。
- 增加 lease 过期扫描；`executionMatches` 校验 lease 未过期。
- `RoutedTaskExecutor` 在 runner error 时强制 `StatusFailed`。
- `DefaultTaskExecutor` 不再返回假成功。
- Artifact：
  - 移除 `"artifact generated for ..."` 兜底；
  - 空值/缺失输出使任务失败；
  - 禁止重复 artifact producer。
- `RetryTask` 重置 `RunCount`，限制运行中 retry/reassign。
- 增加 scheduler 状态机、lease、artifact 回归测试。

## Phase 3 — P1 前端状态与交互

- 增加 `GET /api/plans/:id` 回填：刷新/切换 session 后同步 plan 状态。
- 节点执行改为计划内接口，审批未通过不可执行。
- SSE `onerror`：释放 `isExecuting`、显示错误。
- 取消只影响非终态节点；补充 `canceled` 样式/标签。
- 重试所有失败节点，而不是只重试第一个。
- 普通计划成功后清空附件；发送中禁止重复提交。
- 移除假全文翻译/假划词翻译，标注“未接入”；PDF 使用上传内容或移除硬编码 arXiv。
- Nginx `client_max_body_size` 与后端一致。
- 修复 `intentLabels`、空状态、Tailwind typography 插件。
- 增加前端 API/SSE/状态测试（至少纯函数 + mock fetch）。

## Phase 4 — 工程化与跨平台

- 添加 `.gitattributes`（LF）、`.editorconfig`、`go.work`。
- 统一 Go 版本（Dockerfile 使用 `golang:1.26.1`）与 Node 版本（Node 22）。
- `npm ci` 替换 `npm install`。
- 增加 `.github/workflows/ci.yml`：
  - Linux Go tests + race + vet；
  - frontend build/lint/test；
  - Python tests；
  - Docker build。
- 明确 Windows 支持范围：若保留 Windows 脚本，则修复或明确标注 backend 仅 Linux/Docker。
- 增加 `LICENSE` 之外的必要治理文件（SECURITY.md / CONTRIBUTING.md，可选）。
- 清理死代码：`internal/Intent`、旧 `frontend/src/App.tsx`、legacy `RegisterPlanRoute`/`GeneratePlan`（或标注弃用）。

## Phase 5 — DeepSeek 接入与容器覆盖

- 创建被 gitignore 的 `research-assistant/backend.env` 与 `.env`；
- 写入：
  - `OPENAI_API_KEY`（用户提供，不提交）
  - `OPENAI_BASE_URL=https://api.deepseek.com`
  - `OPENAI_MODEL_NAME=deepseek-flash`
- `docker compose build --no-cache` 或增量构建；
- `docker compose up -d`；
- 健康检查 `/api/health` 全部 ok；
- 用真实 DeepSeek 调用 `/api/chat` 与 `/api/plan` 做连通性验证。

## Phase 6 — 轻量论文端到端

候选：`Attention Is All You Need` 的 `annotated-transformer` smoke 模式（不跑 WMT14）。
- 在容器内启动 backend/sandbox/optimizer/frontend；
- 通过 API 创建论文复现计划；
- 执行并轮询到终态；
- 验证事件、Artifact、run_metrics、comparison_report、claim evidence；
- 运行 Benchmark 示例与 AutoResearch 示例；
- 生成 `reports/e2e-<timestamp>.json`，不包含密钥。

## Phase 7 — 收尾

- `git status` 干净；
- 汇总所有容器测试结果；
- 更新 README/运行边界；
- 提交最终修复。

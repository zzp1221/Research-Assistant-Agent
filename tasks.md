# Research Assistant Agent — Tasks

## Phase 0 — Git

- [x] git init
- [x] baseline commit `8d5ea95`
- [x] remediation docs commit `ace3c74`
- [x] `.gitattributes` + `.editorconfig`
- [x] `go.work`
- [x] LF normalization of tracked text files

## Phase 1 — P0 安全

- [x] 旧 `/api/execute` 改为 410 tombstone
- [x] 新增 `POST /api/plans/:id/tasks/:taskId/execute`
- [x] 前端改为计划内执行
- [x] 校验 `storage_path` 位于 `UPLOAD_ROOT`
- [x] 校验 `workspace_path` 位于 `SANDBOX_WORKSPACE_ROOTS`
- [x] 拒绝符号链接/路径穿越
- [x] executor 入口统一校验 workspace/code/uploaded 路径
- [x] 工作区权限由旧接口移除，重建目录改为 0700
- [x] 移除残留 0777（旧 mock 链已删除）
- [x] JSON/API 请求体大小限制（plan 2 MiB / chat 1 MiB）
- [x] 沙箱容器 ID 正则校验（dk hex + os 格式）
- [x] 沙箱容器标签校验（sandbox/tenant/task/plan）
- [x] `DeleteSandbox` 返回真实错误
- [x] Compose 移除默认 token
- [x] Compose 端口绑定 loopback
- [x] Compose backend/sandbox 基础加固
- [x] `REQUIRE_API_AUTH` 在 Compose 中强制 fail-closed
- [x] gateway 无 Docker socket；只有内部 executor 持有 socket
- [x] 前端 `VITE_API_AUTH_TOKEN` + 60s HMAC SSE ticket
- [x] `approvePlan` 状态校验
- [x] full reproduction 审批派生
- [x] 安全回归测试（路径、容器 ID、计划内执行、fail-closed）

## Phase 2 — P1 调度/Artifact

- [x] ctx 超时/取消终态
- [x] 启动错误转 plan failed
- [x] lease 过期扫描（含 2s grace）
- [x] execution epoch/lease 校验（ExecutionEpoch 参与 fencing）
- [x] runner error 强制 Failed
- [x] 移除 DefaultTaskExecutor 假成功
- [x] 禁止空 Artifact
- [x] 禁止重复 Artifact（同任务重复 + 跨任务 producer）
- [x] 单节点执行落库（状态、结果、Artifact）
- [x] RetryTask 重置 RunCount
- [x] API 层禁止运行中 retry/reassign
- [x] scheduler 回归测试
- [ ] 多实例文件锁/lease scanner（单机范围外）

## Phase 3 — P1 前端

- [x] GET plan 状态回填（SSE 终态 + session 切换）
- [x] 节点执行走计划接口
- [x] SSE error 释放状态
- [x] cancel 只改非终态节点
- [x] canceled 样式/标签
- [x] retry 全部失败节点
- [x] 普通计划成功后清空附件
- [x] 发送中禁止重复提交
- [x] 假翻译标注未接入
- [x] PDF 改为读取上传内容，移除硬编码 arXiv
- [x] 审批未通过时禁用节点执行按钮
- [x] 页面 focus 时再次回填计划状态
- [x] Nginx client_max_body_size 与后端上传上限对齐
- [x] intentLabels 补全
- [x] GraphPanel 空状态
- [x] Tailwind typography 插件
- [ ] 前端 API/SSE 组件测试（当前有解析器 + PDF 纯函数测试）

## Phase 4 — 工程化

- [x] `.gitattributes`
- [x] `.editorconfig`
- [x] `go.work`
- [x] Go 1.26.1 Dockerfile
- [x] Node 22 Dockerfile
- [x] `npm ci`
- [x] CI workflow（Linux + Windows job）
- [x] Windows 支持声明（backend Linux-only；sandbox/docker-core/tool 有 Windows job）
- [x] 统一 Go 1.26.1 / Node 22
- [x] gofmt + go vet + backend coverage 门禁
- [x] 清理死代码（`internal/Intent`、旧 `App.tsx`/`App.css`、legacy plan 路由/`GeneratePlan`/`models.Plan`）

## Phase 5 — DeepSeek

- [x] `backend.env`（gitignored）
- [x] `.env`（gitignored）
- [x] DeepSeek API 连通性探测（200）
- [x] `docker compose build`
- [x] `docker compose up -d`（宿主机 18080/15173，避开已占用的 8080）
- [x] health check
- [x] `/api/chat` real DeepSeek
- [x] `/api/plan` real DeepSeek

## Phase 6 — 论文 E2E

- [x] 选择论文与仓库：Apriori / `asaini/Apriori`（纯 Python，轻量）
- [x] 容器内创建计划
- [x] 容器内执行计划（10/10 节点）
- [x] 验证事件/Artifact（15 个频繁项集、5 条关联规则、claim-evidence 图）
- [x] 运行 Benchmark 示例（Go harness 回归通过）
- [x] 运行 AutoResearch 示例（rank_bm25 8/8，hidden holdout 3/3）
- [x] 生成报告 `reports/`

## Phase 7 — 技术债闭环

- [x] 合并两套沙箱实现（删除 `backend/internal/sandboxserver`；gateway/executor 共用 `docker-sandbox`）
- [x] 拆分 2000+ 行生产文件（`autoresearch`、`coder`、`planner` 均低于 2000 行）
- [x] 删除 legacy plan 路径（`RegisterPlanRoute`、`GeneratePlan`、`models.Plan`、legacy response）
- [x] `FilePlanStore` 改为 append-only JSONL 事件存储 + 旧快照迁移
- [x] 删除 `internal/Intent`、旧 `App.tsx`/`App.css`
- [ ] 多副本共享存储 / leader election（单机范围外）

## Phase 8 — 收尾

- [x] Go/前端/容器测试通过
- [x] `docker compose up -d --build` + health check
- [x] `git status` clean
- [x] 最终提交

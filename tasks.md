# Research Assistant Agent — Tasks

## Phase 0 — Git

- [x] git init
- [x] baseline commit
- [ ] `.gitattributes` + `.editorconfig`
- [ ] `go.work`

## Phase 1 — P0 安全

- [ ] 删除旧 `/api/execute` 或改为计划内安全接口
- [ ] 新增 `POST /api/plans/:id/tasks/:taskId/execute`
- [ ] 前端改为计划内执行
- [ ] 校验 `storage_path` 位于 `UPLOAD_ROOT`
- [ ] 校验 `workspace_path` 位于 `SANDBOX_WORKSPACE_ROOTS`
- [ ] 拒绝符号链接/路径穿越
- [ ] 工作区权限 0700
- [ ] JSON 请求体大小限制
- [ ] 沙箱容器 ID 正则校验
- [ ] 沙箱容器标签校验
- [ ] `DeleteSandbox` 返回真实错误
- [ ] Compose 移除默认 token
- [ ] Compose 端口绑定 loopback
- [ ] Compose backend/sandbox 基础加固
- [ ] `REQUIRE_API_AUTH` fail-closed
- [ ] 前端 `VITE_API_AUTH_TOKEN` + SSE ticket
- [ ] `approvePlan` 状态校验
- [ ] full reproduction 审批派生
- [ ] 安全回归测试

## Phase 2 — P1 调度/Artifact

- [ ] ctx 超时/取消终态
- [ ] 启动错误转 plan failed
- [ ] lease 过期扫描
- [ ] execution epoch fencing
- [ ] runner error 强制 Failed
- [ ] 移除 DefaultTaskExecutor 假成功
- [ ] 禁止空 Artifact
- [ ] 禁止重复 Artifact producer
- [ ] RetryTask 重置 RunCount
- [ ] 运行中禁止 retry/reassign
- [ ] scheduler 回归测试

## Phase 3 — P1 前端

- [ ] GET plan 状态回填
- [ ] 节点执行走计划接口
- [ ] SSE error 释放状态
- [ ] cancel 只改非终态节点
- [ ] canceled 样式/标签
- [ ] retry 全部失败节点
- [ ] 普通计划成功后清空附件
- [ ] 发送中禁止重复提交
- [ ] 移除假翻译/硬编码 PDF
- [ ] Nginx client_max_body_size
- [ ] intentLabels 补全
- [ ] GraphPanel 空状态
- [ ] Tailwind typography
- [ ] 前端 API/SSE 测试

## Phase 4 — 工程化

- [ ] `.gitattributes`
- [ ] `.editorconfig`
- [ ] `go.work`
- [ ] Go 1.26.1 Dockerfile
- [ ] Node 22 Dockerfile
- [ ] `npm ci`
- [ ] CI workflow
- [ ] Windows 支持声明/修复
- [ ] 清理死代码

## Phase 5 — DeepSeek

- [ ] `backend.env`（gitignored）
- [ ] `.env`（gitignored）
- [ ] `docker compose build`
- [ ] `docker compose up -d`
- [ ] health check
- [ ] `/api/chat` real DeepSeek
- [ ] `/api/plan` real DeepSeek

## Phase 6 — 论文 E2E

- [ ] 选择论文与仓库
- [ ] 容器内创建计划
- [ ] 容器内执行计划
- [ ] 验证事件/Artifact
- [ ] 运行 Benchmark 示例
- [ ] 运行 AutoResearch 示例
- [ ] 生成报告 `reports/`

## Phase 7 — 收尾

- [ ] 全部容器测试通过
- [ ] `git status` clean
- [ ] 最终提交

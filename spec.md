# Research Assistant Agent — Remediation Spec

## 1. 目标

将当前研究原型按优先级修复到“可在受控容器环境运行、可审计、可复现”的状态，并接入真实 DeepSeek，完成主要功能测试。

## 2. 范围

### 2.1 必须完成

1. Git 基线与可回滚提交。
2. P0 安全修复：
   - 删除/隔离 `/api/execute` 绕过计划治理的直接执行入口；
   - 校验 `task_id`、`storage_path`、`workspace_path`，禁止越出允许根目录；
   - 修复 `task.ID` 拼接 `/tmp` 的路径穿越与 0777 权限；
   - 沙箱容器 ID 强校验 + 标签归属校验，禁止对控制面/任意容器执行；
   - 认证默认 fail-closed（至少容器部署），删除可预测默认 token；
   - Compose 只绑定 loopback，修复 token 插值覆盖问题。
3. P1 调度与正确性：
   - ctx 超时/取消必须落终态；
   - 实现 lease 过期检查与 execution epoch fencing；
   - Agent 返回 error 强制失败；
   - 禁止空/伪造 Artifact 进入下游；
   - 修复 full reproduction 审批绕过与终态复活。
4. P1 前端：
   - 计划状态/审批状态从服务端回填；
   - 节点执行不得绕过计划审批；
   - SSE 错误释放执行状态并提示；
   - 取消/重试前后端状态一致；
   - 移除假 PDF 翻译/硬编码 PDF，或明确标注未接入；
   - Nginx 上传大小限制与后端一致。
5. 工程化：
   - 增加 `.gitattributes`，统一 LF；
   - 增加 CI（Linux 必跑；Windows 明确支持或声明 Linux-only）；
   - 统一 Go/Node 版本；
   - 增加 `go.work`；
   - Dockerfile 使用 `npm ci`，基础镜像固定版本。
6. DeepSeek 接入：
   - 使用用户提供的 API Key/Base URL/Model；
   - 密钥只写入被 gitignore 的 `backend.env`/容器环境，不进入仓库；
   - 重建并启动容器，健康检查通过。
7. 轻量论文端到端：
   - 选择一篇轻量级、可 smoke 复现的论文；
   - 在容器内执行 Planner、Scheduler、Agent、Sandbox、Benchmark/AutoResearch、Claim Evidence 相关链路；
   - 保存运行摘要和失败证据。

### 2.2 非目标

- 不追求完整多租户 OIDC/RBAC；
- 不实现完整 WMT14 等大规模训练；
- 不保证 Windows 原生运行后端（可选择明确声明 Linux-only）；
- 不重写全部历史遗留代码，但必须移除/隔离会误导的假功能与死路径。

## 3. 安全要求

- 任何用户输入不得直接成为宿主机路径；
- 任何沙箱 ID 必须通过格式与标签双重校验；
- 控制面不得接受任意容器名；
- 默认无 token 时不得对外暴露执行接口；
- 所有密钥只存在于运行时环境，不提交 Git；
- 所有项目测试在容器内运行，不在宿主机安装/执行项目依赖。

## 4. 验收标准

1. `git status` 干净，修复提交可回滚。
2. `go test ./...`（backend、docker-sandbox、docker-core）在容器内通过。
3. `go test -race` 关键包在容器内通过。
4. 前端 `npm ci && npm run build && npm run lint && npm test` 在容器内通过。
5. 新增安全回归测试覆盖：
   - `/api/execute` 已移除或拒绝非计划任务；
   - `storage_path`/`workspace_path` 越界被拒绝；
   - 沙箱容器名/非本系统容器 ID 被拒绝；
   - 计划超时/取消后状态为终态；
   - Agent error 不会写成 completed。
6. `docker compose up -d --build` 成功，backend `/api/health` 返回 `ok=true`。
7. 使用真实 DeepSeek 完成至少一次 `POST /api/plan` + `POST /api/plans/:id/execute`，并生成事件/Artifact。
8. 完成一篇轻量论文 smoke 复现，产出 `run_metrics`、`comparison_report` 或等价证据。
9. 运行报告保存于 `reports/`（容器内生成或由容器输出复制），不包含 API Key。

## 5. 测试策略

- Go：`golang:1.26.1` 容器，只读挂载源码；必要时用 Docker volume 缓存模块。
- 前端：`node:22` 容器或临时副本，执行 `npm ci`、build、lint、test。
- Python：`python:3.12` 容器，执行 optimizer/examples 测试。
- 集成：`docker compose` 网络内启动 runner 容器，通过服务名访问 backend。
- 不在宿主机执行项目测试、构建或安装依赖。

## 6. 密钥与数据处理

- `backend.env`、`.env` 均被 gitignore；
- 报告中只写“已配置/健康检查通过”，不输出 Key；
- 不将 Key 写入 Dockerfile、compose、源码或测试 fixture。

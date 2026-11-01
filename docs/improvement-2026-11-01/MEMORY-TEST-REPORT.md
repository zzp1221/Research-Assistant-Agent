# Agent 记忆系统测试报告（feature/memory）

- 日期：2026-11-01
- 环境：Docker sandbox 容器（python:3.11-bullseye + Go 1.26.1 + Node 24.15.0）；真实 compose 栈（backend 18080 / frontend 15173 / sandbox 8082）保持运行并做健康检查
- 约束：所有功能测试在容器内执行；宿主机 shell 无法访问 Docker named pipe，因此通过项目自身的 docker-sandbox API 创建测试容器；未修改 compose 栈数据

## 1. 构建与静态检查（容器内）

| 检查 | 命令 | 结果 |
|---|---|---|
| 后端构建 | `go build ./...` | 通过 |
| 后端 vet | `go vet ./...` | 通过（无输出） |
| 后端格式 | `gofmt -l`（本次改动文件） | 通过（无输出） |
| 后端全量单测 | `go test ./...` | 全部包通过 |
| 前端单测 | `npm test` | 36 个测试文件 / 144 tests 通过 |
| 前端构建 | `npm run build`（tsc + vite） | 通过 |
| 前端 lint | `npm run lint` | 通过（无输出） |

关键新增测试：
- `internal/memory`：FileStore 重开/重放、去重、CJK tokenizer/BM25、owner 隔离、hidden/secret 拒绝、core+retrieved 预算、文档记忆 pending、trial 负结果/程序记忆、consolidation 归档/reflection。
- `internal/session`：create/ensure/list/delete + 跨 owner 拒绝。
- `internal/api`：memory CRUD/search/owner 隔离/删除；chat 多轮 + core memory 注入 + citations。
- `internal/prompts`：core_memory/plan_context/task_instruction/retrieved_memory/task_inputs 顺序与预算。
- `internal/scheduler`：RoutedTaskExecutor 注入 core memory/memory_refs。

## 2. 长对话 E2E（重点）

脚本：`test/e2e/memory-long-chat.py`（真实后端二进制 + 确定性 OpenAI-compatible mock，均运行在同一容器内）。

- 预置 pinned profile：`用户喜欢蓝色`。
- 第 1 轮：`我叫小明，我喜欢蓝色`；第 2 轮：`我喜欢什么颜色？`，返回包含 `蓝色` 且带 citations。
- 继续发送至 30 轮，共 32 次模型请求。

断言结果：
- 第 2 次请求包含第 1 轮 user/assistant 内容（服务端 ChatSession 持久化生效）。
- 32 次请求全部包含 `[core memory]` 与 `用户喜欢蓝色`（core 记忆不被压缩/丢失）。
- 超过 20 条消息窗口后出现 `[earlier turns summary]`（上下文压缩生效）。
- 最大请求消息数 24（历史被 20 条窗口 + 系统/记忆块约束，未无限增长）。
- 最后一次请求仍包含当前用户消息 `第30条`；无 4xx/5xx，任务无报错。

实测输出：

```
PASS chat_multi_turn requests=32 max_messages=24 summary=True
PASS memory_stats total=1 search_hits=1
```

结论：上下文过长压缩后，memory 注入没有造成消息丢失、重复注入、预算溢出或任务错误；摘要 + core + retrieved + 当前消息的顺序稳定。

### 2.1 真实 LLM E2E（使用提供的 Anthropic 兼容凭证）

脚本：`test/e2e/memory-real-llm.py`（后端二进制运行在容器内，直接调用真实 provider，不使用 mock）。

- Provider：`https://api.ai-pixel.online/v1`（OpenAI-compatible `/chat/completions`）。
- 凭证：使用用户提供的 `ANTHROPIC_AUTH_TOKEN` 作为 Bearer key。
- 模型：`deepseek-v4.1-flash`。
- 说明：用户给出的 `deepseek-v4.1-flash-1017` 在该 key 下返回 HTTP 503 `model_not_found`（`No available channel ... group room-1071`）；`GET /v1/models` 只列出 `deepseek-v4.1-flash`，该模型在 OpenAI `/v1/chat/completions` 和 Anthropic `/v1/messages` 均返回 200。

测试内容与结果：
- 3 轮 chat：第 2 轮问“我刚才说我叫什么名字？”→ `小明`；第 3 轮问“我喜欢什么颜色？”→ `蓝色`（core memory + 多轮历史均生效）。
- 继续 12 轮普通消息触发上下文压缩；最后问“我最开始说我叫什么名字？”→ `小明`（压缩摘要保留了早期事实）。
- `GET /api/memories/stats` 与 `POST /api/memories/search` 均正常。

实测输出：

```
turn1: 你记住了
turn2: 小明
turn3: 蓝色
final: 小明
PASS real_llm model=deepseek-v4.1-flash turns=15 memory_total=1 search_hits=1
```

### 2.2 复杂科研长任务 E2E（30 轮真实 LLM + 计划 A/B）

脚本：`test/e2e/memory-complex-research.py`。

- 预置 pinned 项目记忆：Attention Is All You Need、arXiv:1706.03762、WMT14 EN-DE BLEU 27.3、Python 3.11 + PyTorch 2.4 + A100 40GB、torch 版本冲突/固定 torch==2.4.0。
- 30 轮真实 LLM 科研对话，包含“基于上面的论文/环境/失败经验继续”的追问；第 21–24 轮分别询问环境、失败原因、基线、arXiv。
- 计划 A：15 个节点的 Attention 复现计划；计划 B：同 session “继续，基于上面的复现计划再详细一点”，15 个节点，`parent_plan_id` 指向 A，`memory_context` 包含 27.3。

实测输出：

```
env_answer: Python 3.11 / PyTorch 2.4 / A100 40GB
fail_answer: torch 版本冲突，固定 torch==2.4.0
baseline_answer: WMT14 EN-DE BLEU 27.3
arxiv_answer: arXiv:1706.03762
PASS real_llm_complex_chat turns=30 memory_total=1 search_hits=1
PASS real_llm_plan plan_a=<id> parent=<same id> nodes_a=15 nodes_b=15 memory_context_len=730
```

### 2.3 长任务发现并修复的真实缺陷

- 首次运行计划 A 时（带 arXiv 链接、触发 paper import 的路径）`createPlan` 在 `plan_runtime.go:423` 发生 nil pointer panic：`buildResearchContext` 返回 nil + contextErr 时，代码先解引用 `researchContext.Paper` 再检查 `contextErr`。
- 修复：先检查 `contextErr`，再对 `researchContext == nil` 做保护，最后才合并 `importedPaper`。
- 修复后重新构建并运行完整 30 轮真实 LLM 对话 + 计划 A/B：全部通过；`gofmt -l` 无输出；`go test ./...` 全部包通过。

## 3. 计划记忆 / 跨计划 E2E

脚本：`test/e2e/memory-plan-e2e.py`。

- 创建 pinned profile。
- 计划 A：`运行一段 Python 代码并分析结果`，session_id=`s-plan`。
- 断言 `memories?kind=episode` 中存在 `mem-plan-<A.id>`。
- 计划 B：`继续，再详细一点`，同一 session_id。
- 断言 `plan_graph.parent_plan_id == A.id`。
- 断言 `plan_graph.context.parent_plan_summary` 包含计划 A 意图。
- 断言 `plan_graph.context.memory_context` 包含 pinned profile `用户喜欢蓝色`。

实测输出：

```
PASS plan_memory episode=<id> parent=<same id> memory_context_len=129
```

## 4. Compose 栈冒烟

- `GET http://127.0.0.1:18080/api/health`：`ok=true`；sandbox/intent/optimizer 均 ok；native docker available。
- `GET http://127.0.0.1:15173/`：200。
- `GET http://127.0.0.1:8082/api/v1/health`：200。

## 5. 环境边界与未验证项

- 宿主机 shell 的 Docker named pipe 被 DSH 文件沙箱阻断；所有容器测试通过 docker-sandbox API 的 `/api/v1/sandboxes` 完成。
- 真实 compose 栈仍是旧镜像；本轮未重建镜像（需要 Docker CLI 权限）。新代码在容器内以独立二进制完成 E2E，构建链与 compose 使用同一 `go build`。
- `deepseek-v4.1-flash-1017` 在提供的 key 下不可用（HTTP 503 `model_not_found`，group `room-1071`）；`/v1/models` 只列出 `deepseek-v4.1-flash`，使用该模型已完成真实 LLM E2E（见 §2.1）。
- M4 embedding/图检索默认关闭，未做向量消融。

## 6. 清理

- 已删除测试容器（docker-sandbox API DELETE，sandbox_count=0）。
- 已删除宿主临时 tar/pid/marker 文件；保留 `test/e2e/memory-*.py` 与 `test/mock/mock_chat_server.py` 作为可复跑测试资产。
- 未新增 Docker 镜像；仅使用现有 python:3.11-bullseye 镜像创建临时测试容器。

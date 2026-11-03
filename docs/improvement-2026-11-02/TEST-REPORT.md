# Paper Wiki 测试报告（feature/memory）

- 日期：2026-11-02（W3 更新：2026-11-03）
- 环境：容器内（golang:1.27.3-bookworm 构建/单测；python:3.11-bullseye 运行后端二进制与 E2E；node:22-bookworm-slim 前端 build/test/lint）
- 约束：所有功能测试在容器内执行；真实 compose 栈（backend 18080 / frontend 15173 / sandbox 8082）只做只读健康冒烟
- 结论：**通过**。全量后端测试、前端测试、wiki API E2E、真实 LLM 30+ 轮长对话 + 计划 A/B、W3 自动 LLM 摘要与 Link 裁决均通过；未发现新增回归。

## 1. 构建与静态检查

| 检查 | 命令（容器内） | 结果 |
|---|---|---|
| 后端构建 | `go build ./...` | 通过 |
| 后端 vet | `go vet ./...` | 通过（无输出） |
| 后端格式 | `gofmt -l`（本轮改动目录） | 通过（无输出） |
| 后端全量单测 | `go test ./...` | 全部包通过（含 agent/scheduler/api/wiki/memory/prompts/documents） |
| 前端构建 | `npm run build`（tsc + vite） | 通过 |
| 前端单测 | `npm test -- --run` | 37 个测试文件 / 146 tests 通过 |
| 前端 lint | `npm run lint` | 通过（无输出） |

## 2. 新增/关键单测

- `internal/wiki/identity_test.go`：DOI>arXiv>title 优先级、owner 参与 paper_id、DOI 与 arXiv 别名合并、LibraryRecord 别名、hidden 拒绝。
- `internal/wiki/store_test.go`：WAL/snapshot 重开、owner 隔离、列表过滤、归档。
- `internal/wiki/points_test.go`：user_note、paper_parse 摘要 pending/inferred、point_key supersede、metrics result、缺 refs 拒绝。
- `internal/wiki/links_test.go`：实体候选、对称方向去重、confirm/reject、从 memory 重建 PaperPage/Point。
- `internal/wiki/retrieve_test.go`：预算公式、渲染 XML、citation、pending 不注入、预算硬上限截断。
- `internal/memory/refs_test.go`：Refs quote 长度、hidden document、注入降级、Refs/Attributes 持久化与 Patch。
- `internal/documents/provider_doi_test.go`：ApplyProviderRecord 保留 DOI。
- `internal/prompts/wiki_context_test.go`：wiki_context 位于 retrieved 之后、task_inputs 之前，WikiTokens 记录。
- `internal/api/wiki_test.go`：papers CRUD、owner 隔离、hidden 404、points 创建/软删除、links reject、stats、build 202。

## 3. 容器 API E2E（wiki-e2e.py）

脚本：`research-assistant/test/e2e/wiki-e2e.py`（后端二进制 + 临时 data 目录，无 LLM）

覆盖与结果：

```
PASS wiki_identity points links owner_isolation rebuild
PASS wiki_disabled_rollback
```

断言内容：
- 同一论文 DOI 与 arXiv 两条路径得到同一 `paper_id`，PaperPage 带 DOI；
- 跨 owner GET 404、列表 0 条；hidden benchmark 标题 404；
- user_note Point 创建、列表、软删除后不再出现；
- 两篇共享 dataset 实体的论文经 full build 生成 `uses_same_dataset` Link，方向规范化为 from<to，reject 后 archived；
- 删除 `data/wiki` 并重启后，从 memory paper fact 重建 PaperPage，Link 的 reject 决策仍保留（Link 在 memory）；
- `WIKI_ENABLED=false` 时 wiki 路由 404，后端健康正常。

## 4. 真实 LLM 长对话 + 长任务 E2E（wiki-long-chat.py）

脚本：`research-assistant/test/e2e/wiki-long-chat.py`；provider `https://api.ai-pixel.online/v1`，模型 `deepseek-v4.1-flash`（`deepseek-v4.1-flash-1017` 在该 key 下 503 model_not_found）。

- 预置 pinned 项目记忆（论文 arXiv、BLEU 27.3、Python/PyTorch/A100、torch 冲突修复）。
- 创建 PaperPage：Attention Is All You Need（DOI/arXiv/TLDR/dataset 实体）+ 一条 `batch_size=2048, seed=42` 的 user_note Point。
- 32 轮真实对话（30 轮填充 + 2 轮回忆），超过 20 条消息窗口触发 ChatSession 摘要压缩。
- 结果（最终冻结代码）：

```
final_answer: 称呼 小明 / 喜欢的颜色 蓝色 / 正在复现 Attention Is All You Need（arXiv:1706.03762）/ 基线 BLEU 27.3（WMT14 EN-DE）
tldr_answer: Transformer 用纯自注意力（无循环、无卷积）的编码器-解码器架构，在 WMT14 EN-DE 上以 27.3 BLEU 超越当时的 RNN/CNN 模型…
batch_answer: batch_size = 2048，随机种子 seed = 42。
PASS real_llm_wiki_chat turns=32 memory_total=2 wiki_papers=1 wiki_points=1
PASS real_llm_wiki_plan plan_a=ae555df9-261c-4103-987c-3eec990f1984 parent=ae555df9-261c-4103-987c-3eec990f1984 nodes_a=15 nodes_b=15 memory_context_len=897
```

关键结论：
- 上下文压缩后，早期用户事实（姓名/颜色）与项目事实（论文/BLEU）仍能正确回忆；
- 仅存在于 PaperPage TLDR 与 wiki user_note 的信息能被回答，且 `wiki_citations` 非空 → wiki_context 注入生效；
- 计划 A/B 同 session，B 的 `parent_plan_id` 指向 A，`memory_context` 含 27.3，说明计划上下文与记忆在长任务中未丢失；
- 全程无 4xx/5xx、无任务错误。

## 4.5 W3 自动 LLM 摘要与 Link 裁决 E2E（wiki-w3-e2e.py）

脚本：`research-assistant/test/e2e/wiki-w3-e2e.py`；真实 provider `deepseek-v4.1-flash`，`WIKI_LLM_MODE=all`。

- 创建 Attention Is All You Need（仅摘要，无 PDF）→ PaperPage 自动生成 `tldr` 且 `tldr_inferred=true`；
- `GET /api/memories?q=TLDR` 能查到 `wiki_tldr` 记录（证明 TLDR 持久化到 memory，删除 wiki 可恢复且不重复调用 LLM）；
- 创建 BERT 论文后 full build，`GET /api/wiki/links?status=pending_review` 返回 `source=llm` 的 Link（本次 `extends`/`related` 视模型判断），状态 `pending_review`；
- `WIKI_LLM_MODE=off` 的第二个实例创建同论文不生成 TLDR（开关生效）。

实测输出：

```
tldr: Transformer 用多头自注意力替代循环层，在 WMT14 英德翻译上达到 27.3 BLEU，且训练速度快于 RNN 基线。
llm_link: related pending_review 0.9
PASS w3_auto_tldr_and_link_adjudication
PASS w3_llm_mode_off
```

W3 单测（`internal/wiki/summarize_test.go`）：
- `TestMaybeSummarizePaperCreatesTLDRAndVerifiedPoints`：3 个 Point 中 2 个 quote 定位成功、1 个失败保持 pending 且 quote 清空（quote 定位率 2/3）；
- `TestMaybeSummarizePaperRejectsInvalidJSON`：非法 JSON 恰好重试 2 次后失败；
- `TestSummaryModeOffDoesNotCallLLM`：off 模式 0 次调用；
- `TestEnsurePaperRestoresTLDRWithoutCallingLLM`：删除 wiki 后重建 0 次 LLM 调用且 TLDR 恢复；
- `TestAdjudicateLink`：LLM 返回 `contradicts` 被接受、非法 type 被拒绝；
- `TestTextSimilarityCandidates`：文本相似候选生成。

## 5. 测试过程中发现并修复的缺陷

| # | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| 1 | 后端 SIGTERM 无优雅关闭，memory/wiki 文件锁残留 | 重启后 FileStore 获取锁失败并**回退到内存存储**，记忆看似丢失 | cmd/api 增加 signal + server.Shutdown + CloseRuntimeStores；新增 internal/lockfile 以 PID 存活检测清理陈旧锁；E2E 验证 SIGTERM 后锁释放 |
| 2 | wiki handlers 使用全局 MemoryStore 而非 builder 的 memory store | API 测试/嵌入场景 point 404 | PATCH/DELETE point、stats 改用 builder.Memory |
| 3 | chat 对“这篇论文”这类泛指无法命中 PaperPage | wiki 上下文不注入，回答缺少 TLDR/笔记 | matchPapers 在无命中时回退到最近更新的论文（有界 2 篇） |
| 4 | RenderContext 首个 block 可超过硬预算 | 可能突破 PromptLimit 3% 硬上限 | truncateToTokenBudget 按行截断，单测覆盖 |
| 5 | DOI 归一化小写破坏既有 documents 测试期望 | 回归 | normalizeProviderDOI 保留原始大小写，canonical key 单独小写 |

## 6. Compose 栈冒烟

- `GET http://127.0.0.1:18080/api/health`：ok=true（backend/intent/optimizer/sandbox 均 ok，native docker available）。
- `GET http://127.0.0.1:15173/`：200。
- `GET http://127.0.0.1:8082/api/v1/health`：200。

## 7. 未覆盖/已知限制

- W3 已接线；自动摘要依赖部署的模型配置（env-bootstrap 或用户模型 profile），无 provider 时静默降级、不阻塞论文创建。
- W4 embedding/图增强未实现（设计默认关闭、可选）。
- PDF references（W2.5）未实现（默认关闭）。
- 设计 §16.4 的 1 万篇/10 万 Point 性能指标未压测；本轮仅验证功能正确性与预算上限。

## 8. 清理

- 删除临时测试容器 wiki-test、前端依赖卷 wiki-fe-node、`.e2e` 二进制与调试脚本、测试生成的 data 目录；保留 `test/e2e/wiki-*.py` 作为可复跑测试资产。
- 未新增 Docker 镜像（仅使用现有 golang/python/node 镜像）。

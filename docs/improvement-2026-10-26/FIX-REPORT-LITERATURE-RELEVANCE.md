# 文献检索相关性修复报告（2026-10-28）

## 1. 复现

在 compose 网络内用 Node 22 调用真实后端：

```
GET /api/literature/search?q=graph%20neural%20network%20survey&sources=arxiv,openalex,crossref&limit=5&sort=relevance
```

修复前结果（节选）：

| rank | source | year | citations | title |
|---|---|---|---|---|
| 1 | openalex | 2026 | 1 | Human pose estimation based on graph neural network: survey |
| 2 | openalex | 2025 | 58 | Topological Data Analysis in Graph Neural Networks: Surveys and Perspectives |
| 3 | openalex | 2022 | 1335 | Graph neural network for traffic forecasting: A survey |
| 4 | openalex | 2020 | 10971 | A Comprehensive Survey on Graph Neural Networks |
| 5 | crossref | 2024 | - | Graph Neural Network, ChebNet... Tutorial and Survey |

默认 UI 的 `limit=20` 请求还会在 arXiv 慢响应时返回 502：
`{"code":"literature_search_failed","error":"literature canceled: search canceled"}`（30s 超时）。

## 2. 根因

1. **排序逻辑按 provider 分组，完全丢弃 provider 相关性**
   `internal/literature/literature.go` 的 `applyRanking` 在 `sort=relevance`（默认）时调用 `sortResults`；它先按来源顺序 `arxiv → openalex → crossref → github` 分组，组内只按年份倒序。结果是 arXiv 结果永远压过 OpenAlex，OpenAlex 内部又是 2026→2020，而不是相关性。用户看到“第 1 条相关、后面不相关”就是这个原因。
2. **候选池太小**
   每个 provider 只取 `limit` 条（5 条），OpenAlex 真正相关的 "A Comprehensive Survey on Graph Neural Networks" 排在其原生第 9 位，根本进不了候选池；排序层再正确也无从选择。
3. **单 provider 慢响应拖垮整个搜索**
   provider 调用是串行的，且共用 API 30s 超时；arXiv 连接超时/429 会耗尽整个请求预算，其他 provider 已有结果也返回 502。
4. **Crossref 的通用 query 噪声大**
   `query=` 会返回 "Peer Review #1 of ..."、图注片段等；Crossref 元数据也没有引用数。

## 3. 修复

- `Result` 增加 `provider_rank/provider_score`；合并时保留最优 provider 顺序。
- 新增 `rankByRelevance`：基于查询词的 title/abstract 覆盖率、标题命中、短语命中、survey 意图、引用数与 provider 先验做跨 provider 统一排序，不再按来源分组。
- 候选池：每个 provider 取 `max(limit*3, limit+10, ≤50)`，先聚合去重再排序、最后截断到 `limit`。
- provider 并发调用 + 每 provider 独立 `LITERATURE_PROVIDER_TIMEOUT_SECONDS`（默认 15s）；一个 provider 超时只产生 warning，其他结果照常返回。
- Crossref 改用 `query.bibliographic` + `select` 精简字段，并读取 `is-referenced-by-count`。
- OpenAlex 读取 `relevance_score` 作为先验参考。

## 4. 修复后验证

同一查询、`limit=5`、默认来源：

| rank | source | year | citations | title |
|---|---|---|---|---|
| 1 | openalex | 2020 | 10971 | A Comprehensive Survey on Graph Neural Networks |
| 2 | openalex | 2022 | 1335 | Graph neural network for traffic forecasting: A survey |
| 3 | openalex | 2022 | 1372 | Graph Neural Networks in Recommender Systems: A Survey |
| 4 | openalex | 2023 | 768 | A Survey of Graph Neural Networks for Recommender Systems: Challenges, Methods, and Directions |
| 5 | openalex | 2022 | 593 | Explainability in Graph Neural Networks: A Taxonomic Survey |

- `limit=20` 返回 20 条，全部为 GNN survey 且按相关性递减；耗时 14ms（命中缓存时）。
- 其他查询抽查：`large language model reasoning`、`diffusion model image generation`、`federated learning privacy` 的 top-5 均与主题相关。
- 显式 `sort=citations` / `sort=year` 仍按对应字段排序。
- 该网络下 arXiv 不可达（连接超时 ~10s）时：搜索返回 200 + arXiv warning，OpenAlex/Crossref 结果不受影响；不再出现 502。
- Go 单测：`go test ./internal/literature/... ./internal/api/...` 通过；新增回归测试覆盖“跨 provider 不分组排序”“候选池大于 limit”“单 provider 超时仍返回其他结果”。
- E2E：`test/e2e/literature.mjs`、`test/e2e/literature-plan.mjs`、`test/e2e/http-smoke.mjs` 全部通过。

## 5. 已知环境限制

- 当前测试网络的 arXiv export 端点连接超时，因此真实 E2E 中 arXiv 经常以 warning 出现；这是网络可达性问题，不是排序逻辑问题。生产网络可达时 arXiv 会正常参与合并排序。
- Crossref 冷启动较慢（约 7s），已配置独立 provider 预算 15s；若上游持续超时只会降级为 warning，不会让整个搜索失败。

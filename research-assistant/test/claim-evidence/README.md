# Claim-to-Evidence 验收样例

## 相关流程图

- [Agent 执行流程图](../../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../../docs/diagrams/agent-execution-flow.svg)）
- [运行流程图](../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../docs/diagrams/runtime-flow.svg)）

该目录验证 Claim-to-Evidence Graph 的完整链路：测试通过真实 `RoutedTaskExecutor` 依次调用 Librarian 和 Data Agent 的专用任务入口，并检查：

1. `claim_rubric_extract` 在运行结果判定前冻结 Rubric。
2. 后端分配稳定 Claim / Criterion ID 并计算 SHA-256。
3. Scheduler Artifact 路由把 `claim_rubric` 作为 JSON 交给下游。
4. `claim_evidence_build` 只引用真实存在的运行产物。
5. 图 JSON 通过独立 `structured_data` 返回。
6. 最终准则连接 `run_metrics` 和 `comparison_report`，证据覆盖率为 100%。
7. 前端证据图组件通过 lint 和生产构建。

LLM 接口由本地 `httptest` 服务提供固定语义结果，不需要 API Key。

## 运行

```bash
cd research-assistant
bash test/claim-evidence/run.sh
```

`run.sh` 会跑 Go golden test，再执行前端 lint 与生产构建。

只运行 Go golden test：

```bash
cd backend
go test -v ./tests -run '^TestClaimEvidenceExample$'
```

更新 golden graph：

```bash
cd backend
UPDATE_CLAIM_EVIDENCE_GOLDEN=1 go test ./tests -run '^TestClaimEvidenceExample$'
```

## 文件

| 文件 | 作用 |
|---|---|
| `scenario.json` | 论文摘录、运行产物和两个受控模型响应 |
| `expected_graph.json` | 真实 harness 生成并由测试比对的稳定图 JSON |
| `render_preview.py` | 根据 golden graph 离线重绘预览 |
| `run.sh` | 一键运行入口（Go test + 前端 lint/build） |

预览渲染是可选辅助，不参与主验收；可执行性以 Go golden test 和前端生产构建结果为准。

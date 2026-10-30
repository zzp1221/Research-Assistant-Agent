# 质量与验证状态

本文件记录最近一次可复核的构建、测试与已知缺陷。原始证据默认写入 `reports/`（`.gitignore` 只保留 `reports/README.md`），不随仓库提交。

## 最近验证

- 日期：2026-10-30（Asia/Shanghai）
- 代码状态：`master` @ `1282a57` + 第十一轮 N-13 实现（含前十轮工作区实现）
- 环境：Docker Compose 栈；容器内执行 Go/Node/Python 测试；真实 OpenAI-compatible provider

| 层级 | 覆盖 | 结果 |
|---|---|---|
| 后端构建/单测 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过（第八轮新增 api/budget/project/usage/scheduler 用例全绿） |
| 后端 race | agent/conversation、prompts、planner、api、literature、documents、scheduler、modelprofile、modelresolver | 通过 |
| Docker Sandbox | `go build ./...`、`go test ./...` | 通过 |
| Docker Core | `go build ./...`、`go test ./...` | 通过（容器内 6 个含测试包） |
| 前端 | `npm test`、`tsc -b`、`vite build`、`npm run lint`、`npm run check:bundle` | 30 个测试文件 / 128 个测试通过；构建通过；`eslint .` 通过（本轮新增工具/Agent 设置页与 tool_call 节点展示） |
| Intent Recognition | `python -m unittest test_service.py -v` | 8/8 通过 |
| Research Optimizer | `python -m unittest discover -s tests -p 'test_*.py'` | 8/8 通过 |
| 离线示例 | paper-reproduction、autoresearch/intent_router | 2/2 通过 |
| Tool 模块 | `go build ./...` | 通过（无测试文件） |
| 既有 E2E | http-smoke、routing-and-prompts、literature、literature-plan、document-intelligence、model-profiles、verification、sandbox-network、round6 五套 | 全部通过（第八轮复跑 http-smoke） |
| 第八轮专项 E2E | `plan-library.mjs`、`repro-bundle.mjs`、`cost-budget.mjs`、`budget-recovery.mjs` | 全部通过 |
| 第九轮专项 E2E | `plan-revisions.mjs`（A1/A2/A3/A5）、`review-hitl.mjs`（A4/A6/A7/A8/A9） | 全部通过 |
| 第九轮真实浏览器 | Chrome 手工验收：审阅面板、revision 列表、节点 inspector、diff、保存草稿、批准并运行、收件箱 | 通过（截图见 reports/n05-*.png） |
| 第九轮回归 | `http-smoke`、`plan-library`、`repro-bundle`、`cost-budget`、`budget-recovery`、`routing-and-prompts` | 全部通过 |
| 第十轮复核 | 独立复核 N-01/N-02/N-03/N-05；修复 review gate 绕过、revision 绑定、manifest revision、禁用节点、artifact 脱敏、单任务预算等 12 类问题 | 后端/前端全量通过；六套专项 E2E 通过；详见 [FIX-REPORT-ROUND10](improvement-2026-10-26/FIX-REPORT-ROUND10.md) |
| 第十一轮 N-13 | 真 AllowedTools 白名单、ToolRegistry/ToolInvoker、native/HTTP/MCP stdio/MCP HTTP、tool_call 审批、ToolCallRecord 审计/回放、AgentProfile/PromptVersion、设置页工具/Agent tab | 后端全量通过；前端 128 tests + build + lint 通过；`tool-ecosystem.mjs` 44/44；详见 [FIX-REPORT-ROUND11](improvement-2026-10-26/FIX-REPORT-ROUND11.md) |
| Round7 深度测试 | deep API、真实 LLM 场景、任务执行/取消、文档 assist、sandbox 命令、AutoResearch、论文复现 | deep API 58/59；真实场景 18/18；AutoResearch 8/8 节点且 hidden holdout 3/3；论文复现 14/15 节点 |
| 前端 Playwright | 11 个测试 | 10 passed / 1 failed |

## LLM 计费暂停（已解除，2026-10-29 17:26）

- 2026-10-29 上午容器内 LLM provider 返回 403："分组「高速满血v4.1f-1017 #1038」的计费倍率已由 0.05 上调为 0.07，请求已暂停（未扣费）"。
- 用户确认新倍率后已解除；复测结果：
  - 真实 LLM 计划执行（7 节点代码执行 + 绘图 + 视觉校验）：completed，生成 2970×1768 PNG，visual 校验 pass（置信度 0.95）。
  - Round7 真实场景（code 3/3、plot 7/7、paper 4/4、文献 5/5）：全部通过。
  - N-05 专项与既有回归：全部通过。
- 详见 [FIX-REPORT-ROUND9.md](improvement-2026-10-26/FIX-REPORT-ROUND9.md) 第 7 节。

## 已知问题

以下缺陷来自 `reports/eval-2026-10-28/REPORT.md`；状态按当前工作区更新。

| ID | 严重度 | 问题 | 状态 |
|---|---|---|---|
| D1 | P1 | 论文复现的 claim rubric JSON 解码失败（`decode claim rubric: invalid character '#'`），阻塞 claim evidence | 待修复 |
| D2 | P1 | 论文元数据抽取错误（标题/作者） | 待修复 |
| D3 | P1 | intent 服务 `/predict` 偶发 502/超时，拖慢计划创建 | 待修复 |
| D4 | P2 | document assist 非法 `mode` 返回 502，应为 400 | 待修复 |
| D5 | P2 | 文献检索 relevance 排序差，跨 provider 结果不按相关性排序 | 已在工作区修复（见 [FIX-REPORT-LITERATURE-RELEVANCE.md](improvement-2026-10-26/FIX-REPORT-LITERATURE-RELEVANCE.md)），待提交 |
| D6 | P2 | AI 翻译浮层在 1024×500 遮挡 PDF 缩放按钮 | 待修复 |
| D7 | P3 | Playwright 选择器过期 | 已随 `5242ccd` 更新 |
| D8 | P3 | Paper_Reproduction 的 L4 agent planner 回退（`planner node ref is empty`） | 待修复 |
| D9 | P3 | intent `/health` 泄漏 provider/base_url/model | 待修复 |

## 复跑方式

后端与单元测试：

```bash
cd research-assistant/backend && go test ./... && go vet ./...
cd research-assistant/docker-sandbox && go test ./...
cd docker-core && go test ./...
cd research-assistant/frontend && npm test
```

容器内执行 Go 测试（固定工具链）：

```bash
bash research-assistant/scripts/container-go.sh "go test -count=1 ./..."
```

E2E 需要运行中的 Compose 栈。既有脚本在 `research-assistant/test/e2e/`，使用 `RA_API_URL` 与 `RA_API_TOKEN`：

```bash
RA_API_URL=http://127.0.0.1:8080 RA_API_TOKEN=... node research-assistant/test/e2e/http-smoke.mjs
```

部分套件需要额外环境：`document-intelligence` 使用 `PDF_OCR_MAX_PAGES=20`；`model-profiles`/`verification` 使用 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true`；E2E overlay 见 `research-assistant/test/docker-compose.e2e.yml`。

完整报告与日志（本地生成，不提交）：`reports/eval-2026-10-28/REPORT.md`、`reports/eval-2026-10-28/logs/`。
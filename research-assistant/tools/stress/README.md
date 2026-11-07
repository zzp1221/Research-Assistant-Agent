# 记忆系统压力测试工具（tools/stress）

对应文档：[docs/test/2026-11-03-memory-stress-test-plan.md](../../../docs/test/2026-11-03-memory-stress-test-plan.md)

所有用例都在容器内执行（后端二进制 + 确定性 mock provider；真实 LLM 仅用于补充质量检查）。

## 目录

| 文件 | 作用 |
|---|---|
| common.py | HTTP 客户端、后端/ mock 生命周期、证据目录、断言与 verdict 记录 |
| mock_server.py | 确定性 OpenAI 兼容 mock；记录完整 prompt；支持 needle 召回协议与故障注入 |
| run_suite.py | 套件运行器：--suite MB/LC/SC/LT/LP/CP/PF/RB/all，可 --only 指定用例 |
| suites/case_*.py | 8 个套件共 166 条用例的实现 |
| gen_long_paper.py | 生成 325 页合成综述 PDF（40 节、120 条参考文献、30 表、24 图、60 公式） |
| make_report.py | 从运行目录生成 REPORT.md、scorecards、defects、manifest.json |
| real_llm_check.py | 真实 provider 的三条补充质量用例（记忆召回 / 长论文问答 / 跨论文问答） |
| cp25_probe.py / extra_probes.py | 需要独立后端才能稳定复现的隔离/回退用例（CP-25、LC-07、RB-03） |
| cases.json | 由文档配套 CSV 转换的用例清单 |

## 运行方式

在容器（如 `golang:1.27.3-bookworm`，安装 poppler-utils、reportlab、pypdf）内：

```bash
# 1. 编译后端
export PATH=$PATH:/usr/local/go/bin
cd <repo>/research-assistant/backend && go build -o /tmp/ra-backend ./cmd/api

# 2. 生成语料
python3 gen_long_paper.py --pages 320 --out /tmp/stress-tools/survey.pdf

# 3. 运行套件
python3 run_suite.py --suite MB --run-id stress-2026-11-03-r1 --out /tmp/stress
python3 run_suite.py --suite all --run-id stress-2026-11-03-r1 --out /tmp/stress

# 4. 生成报告
python3 make_report.py --run-dir /tmp/stress --run-id stress-2026-11-03-r1   --out ./report --real-dir /tmp/stress-real
```

## 约束与已知限制

- 长任务 72 小时与 7 天浸泡未真实执行；LT 套件用 200 轮对话 + mock 加速，故障注入覆盖 kill -9、重启、锁竞争。
- PF-02 的 10 万条规模本轮以 3 万条执行（容器时间预算）；PF 结果只代表该规模。
- 真实 LLM 用例需设置 `REAL_LLM_API_KEY` / `REAL_LLM_BASE_URL` / `REAL_LLM_MODEL`；本轮使用 `deepseek-v4.1-flash`。
- `backend/cmd/outlinetest` 是 LP-02/LP-15 的文档 Outline/References 测试钩子（内部包无法从模块外导入）。

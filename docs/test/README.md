# docs/test

本目录存放跨模块的测试方案与测试数据说明（测试报告仍在 `docs/improvement-*` 与 `reports/`）。

| 文件 | 内容 |
|---|---|
| [2026-11-03-memory-stress-test-plan.md](2026-11-03-memory-stress-test-plan.md) | 记忆系统压力测试方案 v1.0（长任务 / 长论文 / 交叉论文，166 条用例） |
| [2026-11-03-memory-stress-test-cases.csv](2026-11-03-memory-stress-test-cases.csv) | 166 条用例的机器可读清单（case_id/suite/priority/title/action/pass_criteria/error_or_risk） |
| [2026-11-03-memory-stress-fix-plan.md](2026-11-03-memory-stress-fix-plan.md) | 压测缺陷修复方案 v1.0（19 条缺陷、13 类根因、A~D 四批修复、回归与验收映射） |

**约定：**

- 测试方案文件名格式 `YYYY-MM-DD-<topic>-test-plan.md`；对应 CSV 同名。
- 方案只写“要测什么、怎么判定、怎么执行”；实测结果写入 `reports/` 或后续的 `docs/improvement-*` 报告。
- 修改方案必须同步更新 CSV 与 `docs/README.md` 索引。

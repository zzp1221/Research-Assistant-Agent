# Claim Evidence 数值验收实现

日期：2026-11-08；分支：`feature/agent`。对应 [Agent 能力方案](2026-11-07-agent-capability-improvement.md) 的 AG-03、A12–A15；实现以本说明及当前代码为准。

总体进展见 [六线实施与验收](2026-11-08-agent-capability-implementation.md)。

## 判定规则

`ClaimCriterion.numeric_contract` 随 rubric 一起计算 SHA-256，在测量任务运行前冻结。模型的 `observed_value`、`status` 和理由保存到 `model_proposal`；后端提取的观测、数值比较及最终状态独立保存。旧 rubric 不会被自动补上能通过的容差、单位或实验协议。`quantitative` / `efficiency` claim 即使漏出数值字段，也必须经过后端检查，不能退回模型成功建议。

- `within_tolerance`：绝对容差、相对容差或明确的 `atol + rtol × abs(expected)` 组合。`none` 表示零容差；边界是否包含由 `inclusive` 明确指定。
- `at_least` / `at_most`：无容差时分别比较 `>=` / `<=`；只有冻结配置显式声明容差时才适用允许偏差。`inclusive=false` 使用严格边界。
- 只有相对容差且参考值为零时，必须显式选择 `zero_reference_policy=exact`；其他情况不可验证。
- 比例、百分数、秒和毫秒按固定换算规则处理；未知单位只允许同单位比较。比例必须在 `[0,1]`，百分数在 `[0,100]`；也检查冻结的自定义范围。
- `single` / `mean` / `minimum` / `maximum` 是预先指定的 seed 聚合方式。缺 seed、重复 seed、观测数不符、NaN、Infinity、越界与缺值均不可验证。

比较使用浮点值最短十进制表示对应的有理数计算，避免 `0.85 - 0.84` 的二进制误差改变 `0.01` 的边界；不添加隐藏 epsilon，也不按显示精度四舍五入后判定。

数值未达到目标、且独立证据和协议完整时，criterion 为 `contradicted`，验收任务仍正常 `completed`。这不会触发“修复到绿”的重试。定性 criterion 的必需证据单独检查；某项数值通过不代表整个 claim 已验证。

## 冻结和来源

`claim.numeric-contract/v1` 要求显式配置比较、单位、来源任务/Artifact、dataset/split、协议、代码、环境 hash、seed 集合和聚合方法。`claim_rubric_extract` 可在输入 `claim_numeric_contracts` 中按后端稳定 criterion ID 提供合同，例如 `claim-001.criterion-01`；绑定发生于 rubric 冻结之前。未提供时保留模型提取的已有字段，缺协议的数值条目仅供审阅。

真实数值判定还要求程序提供的 `sharedContext.plan_graph`，不能用任务输入冒充来源。后端检查：

1. 来源为本任务的上游执行节点，类型是论文执行/重跑、Benchmark、AutoResearch 或实验执行；节点已完成，revision 与计划当前 revision 一致。
2. 执行 ID 来自 scheduler 完成时保存的 `completed_execution_id`，并校验 `completed_execution_epoch` 与 `completed_revision`。兼容仍保留执行 ID 的节点；缺来源的旧记录不可验证。
3. rubric 的原始 Artifact、hash、producer 和创建时间正确，且早于该测量任务开始。
4. dataset、协议、环境原件与冻结 hash 一致，且在测量之前固定。执行节点保存的真实代码与冻结代码 hash 一致。
5. metrics/predictions 的 Artifact producer 正是该执行节点，创建时间处于本次执行开始与完成之间；输入 metrics 内容与原件相同。同 revision 前一执行遗留的 Artifact 不会被套用本次执行 ID。仅有文件路径、Artifact 名称或一个 hash 字符串不构成原始证据。

`metric.execution/v1` 是受约束的执行输出，不是可自授信任的 observation。其形状如下（hash 示例须替换为实际 64 位 SHA-256）：

```json
{
  "schema_version": "metric.execution/v1",
  "dataset_sha256": "<dataset原件hash>",
  "split": "test",
  "protocol_sha256": "<冻结协议hash>",
  "code_sha256": "<执行代码hash>",
  "environment_ref": "environment_manifest",
  "environment_sha256": "<环境原件hash>",
  "runs": [{"seed": 42, "unit": "fraction", "metrics": {"accuracy": 0.817}}]
}
```

执行输出可省略 `execution_id`，后端使用 scheduler 保存的执行 ID；若输出声明了另一个 ID，会被拒绝。`MetricObservation` 只由校验后的后端解析器创建，不接受模型或输入自称的 `backend_recomputed`。

## 两种提取方式及可信边界

`benchmark_predictions` 独立重算复用现有 Benchmark 的 accuracy、macro F1、MSE 和 MAE 计算器。冻结 dataset 原件采用 JSONL，每行包含唯一非负 `index` 与 `target`；prediction 原件每行包含 `seed`、`index`、`prediction`。例如：

```json
{"index":0,"target":"positive"}
```

```json
{"seed":42,"index":0,"prediction":"negative"}
```

目标值来自冻结 dataset 原件，不使用执行输出临时构造的标签。每个 seed 必须完整覆盖该 split；重复、缺失或未知 index 被拒绝。prediction 若附带 target，必须与原件一致。classification 不接受对象/数组标签，regression 拒绝非数值和非有限结果。模型自报或程序输出的 `metrics.accuracy` 无法覆盖重算值。

`json_metric` 仅从具有完整来源的执行 JSON 解析数值，比较器仍会给出达标/未达标结果，但 `review_required=true`；最终 criterion 保持 `unverifiable`，graph 为 `degraded` 并触发既有 needs-review 路径。它表示“执行程序报告了这个值”，没有被包装成独立验证。只有完整可重算证据支持最终 `verified` / `contradicted`。

单个 metrics、prediction、dataset 原件的解析资源上限为 8 MiB，单行上限 1 MiB，seed 最多 100 个。超限明确不可验证，不把截断片段当完整数据。

## 用户反馈和兼容

图 JSON 包含 `model_proposal`、`deterministic_verdict`、`observations`，后者保留执行 ID、协议和原件 hash 引用。验证报告显示预期值、观测值、比较方式、容差、split、协议 hash、判定理由与外部指标的审阅边界。现有证据图增加可展开的“数值验收”，模型建议和后端观测分开展示。

现有标准论文执行的 `run_metrics` 可能仍为 stdout，Benchmark/AutoResearch 也各有既有评测格式。本实现不把这些任意内容自动转成可信 observation，也不迁移隐藏标签到公开记忆。需要实际运行脚本/数据入口提供上述结构化原件与冻结合同，才能走新的独立 Claim 验证。无法满足时仍生成可审阅图和报告，不虚构验证成功。

现有 `claim.evidence/v1` / `claim.rubric/v1` 增加可选字段；旧记录仍可读取。旧 scalar 验收样例改为明确的反例：模型宣称 verified，但缺冻结协议和来源，后端应 unverifiable。证据覆盖率继续只表示链接覆盖，不代表实验准确率。

## 验证

全部测试在 `agent-capability-tests` 容器及一次性 Node 容器运行；使用本地确定性 HTTP fixture，不调用付费模型。

- `claimverify`：单位、方向、容差组合、零参考值、边界精度、非法值/范围、完整 seed 聚合。
- `agent` Claim 回归：模型假成功、独立原始预测重算、正确负结果、完整来源/执行前冻结、错误 split/hash、缺 seed/原件/代码、陈旧 revision/epoch、伪造输入、外部自报指标待审、定性缺证据、目标标签伪造和 split 覆盖不足。
- `tests/TestClaimEvidenceExample`：旧 scalar + 模型成功提案仍为不可验证，原始引用与独立 JSON 路由保持有效。
- 前端：数值验收展示区分模型建议、后端观测和待审状态；类型检查及组件回归。

本轮验证不声称已完成真实论文数值复现或已测得科学结论可靠性提升；这些需选定真实协议、数据与成本预算另行评测。

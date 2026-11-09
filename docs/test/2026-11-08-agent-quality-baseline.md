# Agent 任务质量评测基线

日期：2026-11-08；分支：`feature/agent`；grader：`agent-eval/v1`。

本报告对应 AG-05；跨工作线配置、最终整体验收与未交付范围见 [六线实施与验收](../design/2026-11-08-agent-capability-implementation.md)。

本次报告记录离线契约验证与实际 native 工具执行，不代表真实模型的任务成功率或科研质量有所提升。

## 模式和分母

- `template` 只检查显式变量绑定或指定文本，不能证明模型或 Agent 完成任务。`check_rendering=true` 是显式的模板 grader。
- `model` 必须调用模型且获得非空、完整、可评分的文本；缺 completer、nil/空响应、被复用的响应、未完成生成或工具请求均不借用模板作为响应。
- `task` 调用生产 `ProfileRunner`，经同一 `ToolInvoker` 执行授权工具，再独立检查任务状态、JSON Schema/参考值、工具调用和关联 Artifact。参考值不进入模型上下文。

每个用例记录 `passed | failed | error | skipped`、执行模式、grader 版本和执行标记。报告同时保存全部计划用例数、已尝试、已评分、error 和 skipped；`scored_rate=passed/scored`，`planned_rate=passed/total`。没有已评分用例时 `scored_rate=null`、`score_available=false`，前端显示 N/A。旧 `score` 数值仅为兼容字段，不能在 `score_available=false` 时解释为实测分数。

## 冻结离线集

数据集：`research-assistant/backend/internal/agentprofile/testdata/task-quality-v1.json`；版本 `task-quality/v1`。

SHA-256：`9e218cdb0167b2a3b90c4d297c5827301530d6424a12a1af2586c91462c72d82`。

| 场景 | 冻结预期 | 实际 |
|---|---|---|
| 平均值正确 | 通过 | 通过 |
| 成功关键词，但数值错误 | 失败 | 失败 |
| 脚本失败，但 JSON 声称成功 | 失败 | 失败 |
| 数字字段被写为字符串 | 失败 | 失败 |
| 声称调用工具，但没有执行记录 | 失败 | 失败 |
| Artifact 名称存在，但内容 hash 不符 | 失败 | 失败 |
| 正确报告科学负结果 | 通过 | 通过 |
| 声称阻塞，但缺少约定证据 | 失败 | 失败 |

8/8 个 grader 预期吻合，其中 2 个正例通过、6 个反例被拒绝；该测试集的假通过为 0/6。这里的 8/8 指判分器符合冻结预期，不是 Agent 在真实科研任务上有 100% 成功率。

## 执行验证

后端验证均在 Docker 容器 `agent-capability-tests` 内执行：

```text
go test -race ./internal/agentprofile ./internal/api
go test ./internal/agentprofile -run 'TestFrozenTaskQualityBaseline|TestTaskEvaluatorRunsProfileToolLoopAndHardGrader|TestEvaluatorModelCannotPassUsingTemplateOrEmptyResponse' -count=1 -v
```

生产 ProfileRunner + ToolInvoker 的闭环评测使用确定性模型替身和真实 `native:echo` 工具：返回工具结果后，正确 JSON 通过，带成功关键词的错误数值失败。实际工具调用及 Artifact 均由运行器产生；没有以模型自述或 dry-run 代替执行。另测 grader 参考答案不进入模型消息、工具参数 hash 被篡改、错误调用关联、dry-run、缺 grader、typed-nil completer、nil/空响应、执行异常和零分母。

追加的真实运行器控制任务覆盖显式空 allowlist、达到模型步数上限和写文件工具等待审批；三例均按冻结的预期停止状态通过，审批前未产生文件。`forbidden_tools`、`max_model_steps`、`max_tool_calls` 和 `expected_stop_reason` 是确定性判分项；显式失败/等待结果只在用例预先指定相同 `expected_status` 且所有控制断言均通过时计通过。评测克隆保持显式空工具集合，不因 JSON `omitempty` 把空集合扩大为未声明约束。

前端检查在 `node:22-bookworm-slim` 容器执行：生产构建、bundle 预算、ESLint、21 项单元测试和 41 个组件文件中的 162 项测试均通过。随后新增执行详情组件，2 项针对性测试验证真实 API 到侧栏的运行/恢复诊断映射；TypeScript、对应 ESLint 与相关侧栏回归通过。评测组件验证模板检查显式提供 grader，以及零可评分用例显示 N/A/error。

2026-11-09 提交前复验：在一次性 Node 容器内复制当前源码，按锁文件 `npm ci` 后，TypeScript/生产构建、bundle 预算、ESLint、21 项 unit 与全部 43 个组件文件中的 165 项测试均通过，包含执行详情和 Claim 数值反馈新增用例。提交树 UTF-8 检查通过。评测模型选择器标为“Prompt 评测模型”，并明确 AgentProfile 使用其绑定模型；没有因此改变 Profile 的执行模型。后端本轮全包测试、vet 及包含 agentprofile/API 的扩大 race 均通过，具体范围见总报告。上述为新的完整复验，前一段保留 2026-11-08 分阶段检查记录。

## Task 模式请求示例

对已经绑定 Prompt、模型和 echo grant 的 Profile 调用 `POST /api/agent-profiles/:id/eval`：

```json
{
  "mode": "task",
  "cases": [{
    "name": "echo-and-json",
    "variables": {"task": "调用 echo 读取消息 two，再输出 answer 数值 2 的 JSON"},
    "task": {
      "description": "调用授权 echo 工具后返回 JSON",
      "contract": {"version": "v1", "allowed_tools": ["native:echo"]}
    },
    "grader": {
      "output_schema": "{\"type\":\"object\",\"required\":[\"answer\"],\"properties\":{\"answer\":{\"type\":\"number\"}}}",
      "expected_json": {"/answer": 2},
      "required_tools": ["native:echo"]
    }
  }]
}
```

`expected_json` 使用 JSON Pointer，支持对象和数组路径。要求 Artifact 时提供其内容 SHA-256，不能只检查文件名；要求工具时检查已完成、非 dry-run 的真实调用、参数 hash、结果 hash 和绑定同一调用的原始 Artifact。运行记录包含实际模型 ID/revision、run 引用和独立 usage；缺价格时沿用费用未知标志。

## 边界

未调用付费模型；未执行真实科研沙箱、真实仓库评测、隐藏 holdout 或 memory on/off 质量对照。真实模型/科研质量、恢复成功率、长任务约束保留率、记忆净收益和 p50/p95 延迟仍没有可报告样本，显示 N/A。后续扩展固定输入、模型/环境版本、独立参考答案和费用上限，不能从本次离线结果推断生产提升百分比。

Task 模式从 `/agent-profiles/:id/eval` 发起；Prompt 端点的 task 模式需提供绑定该 Prompt 的 `profile_id`。评测 run/session 使用独立 `eval-*` 命名空间；结果不修改真实研究任务状态，也不写入研究记忆。

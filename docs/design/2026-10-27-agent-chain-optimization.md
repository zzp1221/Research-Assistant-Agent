# Agent 链路优化设计：General 计划、Prompt 组装、Repair 会话与上下文预算

- 状态：设计稿（待评审）
- 日期：2026-10-27
- 范围：backend/internal/planner、scheduler、agent、prompts、usage、modelprofile
- 关联文档：
  - [意图复杂度路由与计划生成选择设计](intent-complexity-routing.md)：L0-L4 复杂度路由总设计
  - [Agent 链路与 LLM 缓存命中率审计](../../reports/agent-pipeline-and-cache-review.md)：本次优化的实测数据与问题清单
- 一句话：先做四个低耦合、可独立验收的改造，降低 General 计划的无效 LLM 调用、消除 prompt 重复、让 repair 循环可复用上下文、并建立 token/context 预算；它们也是后续复杂度路由和缓存优化的前置条件。

---

## 1. 摘要

本设计覆盖四个具体问题：

| 编号 | 问题 | 当前表现 | 目标 |
|---|---|---|---|
| A | General 计划默认 3 个 LLM 节点 | 简单“你好”也生成 Scope、Collect Background、Process 三个节点，3 次串行 LLM | 简单请求 1 个 answer 节点；研究型请求 2 个节点；论文/文献路径保持现有形状 |
| B | Prompt 重复拼装 | task.Description 里已有用户原始意图，buildTaskDescription 又追加 Global user intent；artifact 先 500 字摘要、后完整内容 | 单一 prompt 组装入口；plan context、task instruction、inputs、volatile 各出现一次 |
| C | Repair 循环无状态 | PaperDebug / BenchmarkAdapter / AutoResearch / Planner repair 每次都重建 prompt，不保留上一轮 assistant 输出 | 多轮 conversation session；保留 assistant 上一轮 code/patch/proposal；只追加反馈 |
| D | Token / context 预算缺失 | 每 value 限制、无总预算；AutoResearch 8×96KB、PaperDebug 256KB；byte 截断切坏中文 | 按模型 context window 做 section 级 token 预算；rune-safe 截断；记录预算报告 |

四个改造的共同目标：

1. 减少无效 LLM 调用与 prompt token；
2. 让稳定内容形成可复用的 prefix，提高 provider cache 命中率；
3. 避免 context overflow 和“截断后模型看到坏数据”；
4. 保持现有 API、计划存储、scheduler/executor 协议不变；
5. 全部可灰度、可回滚、可观测。

---

## 2. 通用原则

1. **确定性优先**：能用规则/模板决定的，不调用 LLM；General 计划形状不应由 LLM 决定。
2. **稳定前缀优先**：prompt 顺序必须是“全局稳定 → 计划稳定 → 任务稳定 → 输入 → 易变反馈”，不要把日志、错误、repair 序号放在最前面。
3. **单一组装入口**：prompt 只在一个包/函数里组装，agent 不再各自拼 <code>task.Description + 上游输入</code>。
4. **预算显式化**：每个 prompt 有明确的 token budget、section 优先级和截断报告，不依赖“每个 value 6000 字”这种局部限制。
5. **可观测**：每个优化都记录 plan meta / task metadata / metrics，能对比改造前后的节点数、prompt tokens、cache hit、失败率。
6. **保守回退**：任何新路径失败都回退到现有标准模板/现有行为，不把简单路径作为复杂任务的唯一路径。

---

## 3. 优化点 A：General 计划默认 3 个 LLM 节点

### 3.1 问题在哪

当前 General 请求在 <code>internal/planner/planner.go</code> 的 <code>buildGeneralNodesV2</code>（1499-1537 行）里固定生成：

1. <code>Scope Request</code> → <code>general_process</code> → <code>general_agent</code>
2. <code>Collect Background Context</code> → <code>general_research</code> → <code>librarian_agent</code>
3. <code>Process Request</code> → <code>general_process</code> → <code>general_agent</code>

具体问题：

- **简单请求也被放大 3 倍**：一个“你好”“解释 Transformer”也会走 3 个串行 LLM 节点。每个节点都有独立 system prompt、独立 user prompt、独立失败/超时/重试。
- **两个节点实际是同一个 ChatAgent**：<code>internal/api/routes.go:108-123</code> 把 <code>chatAgent</code> 作为 <code>specialized[2]</code> 传入；<code>internal/scheduler/executor.go:81-85</code> 把 <code>specialized[2]</code> 映射为 <code>General</code> runner；<code>executor.go:277-281</code> 的 <code>general_agent</code> 就是 ChatAgent。ChatAgent 带 <code>[CODE_EXECUTION_REQUIRED]</code> 自动执行代码逻辑（<code>agent/chat.go:99-118</code>），General 计划因此继承了“无审批代码执行”的风险。
- **Scope Request 的产出价值低**：它只产出 <code>intent_scope</code> / <code>assumption_log</code>，下游 t2 消费后再产出 <code>research_notes</code>，t3 再消费。对简单问答，这些中间 artifact 没有实际信息增量。
- **节点合并逻辑救不了它**：<code>internal/planner/agent_planner.go:167-197</code> 的 <code>mergeConsecutiveGeneralProcess</code> 只合并“连续且同类型”的节点；t1 和 t3 中间隔着 t2，所以不会被合并。
- **关键词启发式有误判**：<code>buildGeneralNodesV2</code> 1528 行的 <code>hasAny(normalized, "总结", "summary", "综述", "report", "报告", "rag", "query rewrite")</code> 会把“解释一下 RAG 是什么”也送进 2 节点 research 路径；<code>rag</code>/<code>query rewrite</code> 是主题词，不是“需要调研”的请求信号。
- **真实数据**：<code>reports/**/*plan-final.json</code> 中 General 默认计划 3 个节点（Scope / Collect Background / Process）；General summary 2 个；paper analysis 4-5 个。说明 3 节点是默认路径，不是少数复杂情况。

当前形状汇总：

| 请求类型 | 当前节点 | 当前 LLM 次数 | 问题 |
|---|---:|---:|---|
| 简单问答/闲聊 | 3 | 3 | 过度规划 |
| 总结/报告类 | 2 | 2 | 基本合理 |
| 论文分析 | 4-5 | 4-5 | 基本合理，但 paper 关键词过宽 |
| 文献检索 | 3 | 2-3 | 基本合理 |

### 3.2 为什么这么做

1. **延迟**：3 次串行 LLM 的 P50 通常是 1 次的好几倍；简单请求的用户感知最差。
2. **成本**：3 次 system prompt + 3 次 task/context 重发，token 约 3 倍；且每次都是 one-shot，缓存几乎为 0。
3. **可靠性**：每多一个节点就多一次超时、解析失败、模型 5xx、上下文超限的概率；简单请求不该承担这个失败面。
4. **安全**：t1/t3 走 ChatAgent，可能触发自动代码执行；General 计划不应该默认拥有这个能力。
5. **架构一致性**：<code>planner.BuildPlan</code> 对 General 已经明确采用确定性 builder（64-68 行注释：“General requests always use the deterministic builder: the same prompt must not produce 1/2/3-node plans on different runs”）。3 节点默认路径与这个原则矛盾：形状稳定了，但稳定在一个过度规划的形状上。
6. **缓存**：三个节点 prompt 不同、task 不同，无法共享前缀；1 个节点至少少两次 one-shot。

### 3.3 怎么做

#### 3.3.1 目标形态

| Shape | 触发条件 | 节点 | LLM 次数 |
|---|---|---|---:|
| direct | 无附件、无 paper metadata、无 research/report 信号 | 1 个 <code>general_synthesis</code>（answer_agent） | 1 |
| research | <code>needs_research</code>/<code>needs_report</code>，或明确“总结/综述/调研/报告/对比”，或有附件/文档 | <code>general_research</code> → <code>general_synthesis</code> | 2 |
| paper | <code>isPaperAnalysisIntent</code> 为真 | 保持现有 4-5 节点 | 4-5 |
| literature | <code>isLiteratureDiscoveryIntent</code> 为真 | 保持现有 3 节点 | 2-3 |

原则：**不确定时走 research，不走 direct**。复杂任务被降级为 1 个 answer 节点的代价高于简单任务多一个 research 节点。

#### 3.3.2 新增决策函数

建议放在 <code>internal/planner/general_plan.go</code>（或 <code>planner.go</code> 内）：

~~~go
type generalPlanShape string

const (
    generalPlanDirect     generalPlanShape = "direct"
    generalPlanResearch   generalPlanShape = "research"
    generalPlanPaper      generalPlanShape = "paper"
    generalPlanLiterature generalPlanShape = "literature"
)

func classifyGeneralPlanShape(intent models.IntentContext) generalPlanShape {
    if isLiteratureDiscoveryIntent(intent) {
        return generalPlanLiterature
    }
    if isPaperAnalysisIntent(intent) {
        return generalPlanPaper
    }
    if generalNeedsResearch(intent) {
        return generalPlanResearch
    }
    return generalPlanDirect
}

func generalNeedsResearch(intent models.IntentContext) bool {
    if boolEntity(intent.Entities, "needs_research") ||
        boolEntity(intent.Entities, "needs_report") {
        return true
    }
    if intent.Entities["paper_metadata"] != nil {
        return true
    }
    if _, ok := intent.Entities["uploaded_files"]; ok {
        return true
    }
    lower := strings.ToLower(intent.RawIntent)
    // 只匹配“请求动作”，不要把 rag/query rewrite 这类主题词算进来
    return hasAny(lower,
        "总结", "综述", "调研", "报告", "梳理", "对比",
        "summary", "survey", "review", "report", "research", "compare",
    )
}
~~~

说明：

- <code>isLiteratureDiscoveryIntent</code> / <code>isPaperAnalysisIntent</code> 复用现有函数，避免行为漂移。
- <code>uploaded_files</code> / <code>paper_metadata</code> 走 research 或 paper，不会走 direct。
- 如果后续接入 <code>intent-complexity-routing.md</code> 的 L0-L4，<code>classifyGeneralPlanShape</code> 可以替换为 <code>RouteDecision.Variant</code> 的映射；builder 的目标形态不变。

#### 3.3.3 改造 <code>buildGeneralNodesV2</code>

把当前 1499-1537 行的分支改为：

~~~go
func buildGeneralNodesV2(intent models.IntentContext) []*models.TaskNode {
    context := intent.RawIntent
    switch classifyGeneralPlanShape(intent) {
    case generalPlanLiterature:
        // 保持现有 literature 3 节点
    case generalPlanPaper:
        // 保持现有 paper analysis 4-5 节点
    case generalPlanResearch:
        t1 := newNode("Collect Background Context", "general_research", "librarian_agent",
            nil, nil, []string{"background_context"}, true, context)
        t1.InputBindings = documentBindingsIfPresent(intent)
        t2 := newNode("Synthesize Response", "general_synthesis", "answer_agent",
            []string{t1.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
        return []*models.TaskNode{t1, t2}
    default:
        t1 := newNode("Answer Request", "general_synthesis", "answer_agent",
            nil, nil, []string{"general_response"}, true, context)
        t1.InputBindings = documentBindingsIfPresent(intent)
        return []*models.TaskNode{t1}
    }
}
~~~

<code>documentBindingsIfPresent</code> 负责在有附件/论文时补：

~~~go
[]models.InputBinding{
    {Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
    {Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
}
~~~

注意：

- direct 路径只生成 <code>general_synthesis</code>，它由 <code>answer_agent</code> 执行，taskregistry 已允许（<code>taskregistry.go:201-208</code>）。
- <code>ensureVerificationNode</code> 只对 image producer 补 verify，不会把 direct 1 节点变成 2 节点。
- <code>general_process</code> / <code>general_agent</code> 保留给 agent planner 或自定义计划；默认 General 不再使用。
- 如果 <code>general_synthesis</code> 的 <code>RequiredArtifacts</code> 为空，<code>AnswerAgent</code> 仍能用 <code>task.Description</code> 回答；有附件时通过 InputBindings 注入。

#### 3.3.4 灰度与回滚

- 新增环境变量 <code>GENERAL_PLAN_FAST_PATH</code>，默认 <code>true</code>；<code>false</code> 时走旧 3 节点逻辑。
- <code>GraphMeta</code> 增加 <code>GeneralPlanShape string</code>（或写入 <code>plan.Meta</code> 的扩展字段），记录 direct/research/paper/literature。
- 指标：
  - <code>plan_general_shape_total{shape}</code>
  - <code>plan_llm_nodes_total{intent_type,shape}</code>
  - <code>plan_create_duration_seconds{intent_type,shape}</code>
  - <code>plan_success_total{intent_type,shape}</code>
- 回滚：环境变量关闭即可；旧计划 JSON 不受影响（只是不再生成 3 节点新计划）。

#### 3.3.5 测试与验收

单元测试（<code>internal/planner/planner_test.go</code>）：

| 输入 | 期望 shape | 期望节点 |
|---|---|---|
| 你好 | direct | 1 个 general_synthesis |
| 解释一下 Transformer 的多头注意力 | direct | 1 |
| 总结 RAG 技术的最新进展 | research | 2 |
| 帮我调研一下向量数据库选型并写报告 | research | 2 |
| 分析这篇论文的创新点和局限（带 paper_metadata） | paper | 4-5 |
| 找一下 RAG 相关论文 | literature | 3 |

验收：

- 简单 General 计划节点数 = 1，LLM 节点 = 1；
- research 计划节点数 = 2，LLM 节点 = 2；
- paper/literature 计划形状与改造前一致；
- <code>go test ./internal/planner</code> 全绿；
- 计划创建 P95 相对当前下降 ≥30%（简单 General 样本）；
- 不出现 <code>general_agent</code> 作为默认 General 路径。

---

## 4. 优化点 B：Prompt 重复拼装

### 4.1 问题在哪

当前一个 task 的 prompt 至少经过三层拼接：

1. **planner 生成 <code>task.Description</code> 时已经包含用户原始意图**：
   - 模板 builder：<code>newNode</code> 调用 <code>prompts.TaskDescription(displayName, context)</code>，<code>context = intent.RawIntent</code>（<code>planner.go:174-180</code>、<code>prompts.go:809-811</code>）。
   - agent planner：<code>buildPlannerNodeDescription</code> 调用 <code>prompts.PlannerNodeDescription(...)</code>，同样包含“用户原始意图”（<code>prompts.go:805-807</code>）。
2. **executor 的 <code>buildTaskDescription</code> 又追加 Global user intent**（<code>scheduler/executor.go:379-384</code>）：
   ~~~text
   task.Description
   +
   Global user intent: <plan.UserIntent>
   Plan intent type: <plan.IntentType>
   Available upstream artifacts: <每个 required artifact 前 500 字节>
   Expected outputs: ...
   ~~~
3. **agent 自己又 append 完整 <code>task.Inputs</code>**：
   - <code>agent/data.go:142-145</code>：<code>task.Description + "\n\n上游输入:\n" + formatTaskInputsForPrompt(inputs, 6000)</code>
   - <code>agent/librarian.go:124-127</code>：同样 6000
   - <code>agent/answer.go:91-93</code>：20000
   - <code>agent/coder.go:470-483</code>：12000
   - <code>agent/input_summary.go:14-35</code>：只限制每个 value，不限制总长度

结果：同一段内容在 prompt 里出现两次甚至三次。

一个典型 before 示例：

~~~text
任务目标: 总结 RAG 技术进展
具体要求: 总结 RAG 技术进展
用户原始意图: 总结 RAG 技术进展          ← 第一次
Global user intent:
总结 RAG 技术进展                        ← 第二次
Plan intent type: General
Available upstream artifacts:
- background_context: <前 500 字>         ← 摘要
...
上游输入:
- background_context: <完整内容>          ← 完整内容又一遍
...
~~~

问题不仅是 token 浪费：

- **缓存**：重复内容让 prompt 更长，stable prefix 占比更低；同一 artifact 的 500 字摘要和完整内容不一致，破坏前缀。
- **质量**：模型会看到重复指令/重复材料，容易输出“复述任务”或重复段落；artifact 摘要和完整内容冲突时模型不知道该信哪个。
- **上下文**：重复内容可能把真正重要的材料挤出 context window。
- **维护**：每个 agent 自己决定 per-value limit（6000/12000/20000），行为不一致，测试难写。

### 4.2 为什么这么做

1. **直接降 token**：同一 artifact 只出现一次，简单计划可减少 10-30% prompt token；多 artifact 计划更多。
2. **提高缓存命中**：prompt 前缀稳定、内容唯一，provider 的 prefix cache 更容易命中。
3. **减少上下文溢出**：预算只对一份内容计算，不会因为重复而提前触顶。
4. **提升输出质量**：模型只看到一份权威输入，减少“摘要 vs 全文”冲突。
5. **单一维护点**：以后加 artifact 类型、改截断策略只改一个包。

### 4.3 怎么做

#### 4.3.1 目标结构

把 task prompt 拆成五个 section，顺序固定：

| Section | 内容 | 稳定性 | 是否可截断 |
|---|---|---|---|
| system | BaseSystemPrompt + agent role | 全局稳定 | 否（必须完整） |
| plan_context | user intent、intent type、附件摘要、paper metadata | 计划内稳定 | 否（可限长） |
| task_instruction | 节点 name/type/description、expected outputs | 任务内稳定 | 否（可限长） |
| task_inputs | artifact 内容，按 key 排序 | 任务内稳定 | 是（按预算） |
| volatile | 日志、错误、repair 反馈、ledger | 每次可能变 | 是（优先截断） |

#### 4.3.2 新增 <code>internal/prompts/task_prompt.go</code>

~~~go
type TaskPromptInput struct {
    Plan        *models.PlanGraph
    Task        *models.TaskNode
    Inputs      map[string]any
    Volatile    string
    Budget      PromptBudget
}

type TaskPrompt struct {
    System     string
    User       string
    Report     PromptReport
}

func BuildTaskPrompt(in TaskPromptInput) (TaskPrompt, error)
~~~

内部逻辑：

1. <code>PlanContext(plan)</code>：只放计划级稳定信息，不放大段 artifact：
   ~~~go
   func PlanContext(plan *models.PlanGraph) string {
       var b strings.Builder
       b.WriteString("Plan intent type: " + plan.IntentType + "\n")
       b.WriteString("User intent:\n" + plan.UserIntent + "\n")
       if plan.Context != nil && plan.Context.Paper != nil {
           b.WriteString("Paper metadata:\n" + mustJSON(plan.Context.Paper) + "\n")
       }
       // 附件只放 name/role/id，不放正文
       return b.String()
   }
   ~~~
2. <code>TaskInstruction(task)</code>：只放节点本地信息：
   ~~~go
   func TaskInstruction(task *models.TaskNode) string {
       return fmt.Sprintf("Task: %s\nType: %s\nDescription: %s\nExpected outputs: %s",
           task.Name, task.Type, task.Description, strings.Join(task.OutputArtifacts, ", "))
   }
   ~~~
3. <code>TaskInputs(inputs, budget)</code>：调用新的 <code>formatTaskInputsForPromptBounded</code>，按预算输出一次；图片/base64 仍然省略。
4. 组装：
   ~~~go
   user := strings.Join([]string{
       planContext,
       taskInstruction,
       taskInputs,
       volatile,
   }, "\n\n")
   ~~~

#### 4.3.3 Executor 改造

<code>internal/scheduler/executor.go:371-411</code> 的 <code>buildTaskDescription</code> 改成只返回 node-local：

~~~go
func buildTaskDescription(plan *models.PlanGraph, task *models.TaskNode) string {
    if task == nil { return "" }
    return task.Description // 只保留 planner 生成的节点本地描述
}
~~~

删除：

- <code>Global user intent</code> 追加（379-384 行）
- <code>Available upstream artifacts</code> 500 字摘要（386-399 行）
- <code>truncateArtifactValue</code>（561-566 行）在 prompt 路径上的使用

<code>RoutedTaskExecutor.ExecuteTask</code>（<code>executor.go:90-126</code>）在构建 <code>runtimeTask</code> 时，把 <code>prompts.PlanContext(plan)</code> 放到 <code>sharedContext["plan_context"]</code>，或者直接作为 <code>runtimeTask.Description</code> 的前缀（推荐前者，避免 task 描述被污染）。

#### 4.3.4 Agent 改造

四个 agent 的输入拼接统一替换为 <code>prompts.BuildTaskPrompt</code>：

| 文件/函数 | 当前 | 改为 |
|---|---|---|
| <code>agent/data.go:142-145</code> | <code>task.Description + "\n\n上游输入:\n" + formatTaskInputsForPrompt(6000)</code> | <code>BuildTaskPrompt(...).User</code> |
| <code>agent/librarian.go:124-127</code> | 同样 6000 | 同上 |
| <code>agent/answer.go:91-93</code> | 20000 | 同上 |
| <code>agent/coder.go:470-483</code> | 12000 | 同上 |

<code>BuildTaskPrompt</code> 内部只 append 一次 <code>task.Inputs</code>；agent 不再自己拼“上游输入”。<code>DataAgent</code> 的 <code>runVerification</code> / <code>Librarian</code> 的 retrieval 片段作为 <code>Volatile</code> 或 <code>TaskInputs</code> 传入，不重复。

#### 4.3.5 Before / After

Before：

~~~text
[system] ...
[user]
任务目标: 总结 RAG 技术进展
具体要求: 总结 RAG 技术进展
用户原始意图: 总结 RAG 技术进展
Global user intent: 总结 RAG 技术进展
Plan intent type: General
Available upstream artifacts:
- background_context: <500 字摘要>
...
上游输入:
- background_context: <完整 20KB>
~~~

After：

~~~text
[system] BaseSystemPrompt + role
[user]
Plan intent type: General
User intent: 总结 RAG 技术进展
Paper metadata: ...

Task: 总结 RAG 技术进展
Type: general_synthesis
Description: ...
Expected outputs: general_response

Inputs:
- background_context: <完整 20KB，按预算截断>
~~~

#### 4.3.6 测试与验收

单元测试：

- <code>BuildTaskPrompt</code>：同一 artifact 只出现一次；<code>Global user intent</code> 不重复；输入顺序稳定。
- <code>formatTaskInputsForPromptBounded</code>：中文/英文/图片/base64/嵌套 map 均正确。
- 对 paper reproduction 的 14 节点计划做 golden snapshot：每个 prompt 的 section 顺序固定、无重复文本。

验收：

- 同一计划改造后 prompt token 下降 ≥10%（简单 General 可到 30%）；
- 同一 artifact 内容在 prompt 中出现次数 = 1；
- <code>go test ./internal/agent ./internal/prompts ./internal/scheduler</code> 全绿；
- 现有 E2E 计划输出质量不下降（人工抽检 + eval）。

---

## 5. 优化点 C：Repair 循环无状态

### 5.1 问题在哪

当前所有 repair 循环都是“每次重新构造完整 prompt”，而不是 conversation：

| 循环 | 入口 | 当前做法 | 问题 |
|---|---|---|---|
| PaperDebug | <code>agent/paper_debug_harness.go:85-184</code>、<code>241-253</code> | 每次 <code>planPaperCodeRepair</code> 重新构造 <code>task.Description + diagnostic + contextText + repairNumber</code> | 不保留上一轮 assistant 的 patch；repairNumber 在第一个 token，前缀每次不同；source 在 diagnostic 后面，256KB 不能缓存 |
| BenchmarkAdapter | <code>agent/benchmark_harness.go:26-154</code>、<code>538-558</code> | 每次 <code>repairBenchmarkAdapter</code> 重新发 <code>manifest + spec + failure + code</code> | 不保留上一轮 adapter code 的 assistant 身份；failure 在 code 前，前缀不稳定；repo context 完全不参与 |
| AutoResearch | <code>agent/autoresearch.go:396-742</code>、<code>1396-1408</code> | 每次 <code>proposeAutoResearchCandidate</code> 重新发 spec + ledger + source + readOnly + feedback | 不保留上一轮 proposal；ledger/feedback 在 source 前，source 无法缓存；每轮都重复发送大段 source |
| Planner repair | <code>planner/agent_planner.go:102-124</code> | 重新发“原始意图 JSON + nodes + error” | 不与 initial prompt 共享前缀；上一轮 nodes 只是 user 文本，不是 assistant 输出 |
| Coder self-correction | <code>agent/coder_eino.go:186-191</code> | 只发 error/output，不含上一轮 code | 模型无法看到自己生成的代码；该路径当前基本不可达，但仍是明显缺陷 |

共性：

1. **没有 assistant message**：上一轮的模型输出没有作为 assistant 放回 conversation，模型无法“看到自己刚才写了什么”。
2. **每次从 system 重新开始**：provider 的 prefix cache 只能命中 system 的一小段；后面的大段 source/context 全部 miss。
3. **重复发送大段上下文**：PaperDebug 256KB、AutoResearch 50-200k token、Benchmark repo context 32KB，每轮都重发。
4. **审计困难**：task metadata 只存最终结果，不存 repair 过程；失败时无法复现“第 2 次为什么这么改”。

### 5.2 为什么这么做

1. **质量**：模型看到上一轮 code/patch/proposal，才能做增量修复；否则容易重复同一错误或生成不相关的大改。
2. **缓存**：system + 初始 user + assistant 上一轮输出 + 新 user 反馈，前缀天然稳定；provider 可以命中前面所有 token。
3. **成本**：不需要在新 user 里重新粘贴上一轮代码/补丁；assistant message 只发一次。
4. **审计**：完整 conversation 可以存 artifact，支持回放、对比、人工 review。
5. **可测试**：可以断言第 N 轮 messages 的前缀与第 N-1 轮一致。

### 5.3 怎么做

#### 5.3.1 新增 conversation session

建议新增 <code>internal/agent/conversation/session.go</code>：

~~~go
package conversation

type Session struct {
    ID       string
    Messages []*schema.Message
    MaxTurns int
}

func NewSession(id, system, firstUser string) *Session {
    return &Session{
        ID: id,
        Messages: []*schema.Message{
            {Role: schema.System, Content: system},
            {Role: schema.User, Content: firstUser},
        },
        MaxTurns: 6,
    }
}

func (s *Session) AddAssistant(content string) {
    s.Messages = append(s.Messages, &schema.Message{Role: schema.Assistant, Content: content})
}

func (s *Session) AddUser(content string) {
    s.Messages = append(s.Messages, &schema.Message{Role: schema.User, Content: content})
}

func (s *Session) Generate(ctx context.Context, chat model.BaseChatModel) (*schema.Message, error) {
    msg, err := llmclient.GenerateWithUsage(ctx, chat, s.Messages)
    if err != nil { return nil, err }
    s.AddAssistant(msg.Content)
    return msg, nil
}
~~~

要求：

- <code>Generate</code> 只 append assistant，不重写前面的 messages；
- <code>MaxTurns</code> 控制总轮数；超过时只裁剪中间的 assistant/user 对，**永远保留 system + 第一条 user**；
- <code>Session</code> 序列化到 task metadata 或 artifact（去掉密钥/图片 base64），字段 <code>messages</code>/<code>turns</code>/<code>prompt_hashes</code>。

#### 5.3.2 各循环接入

**PaperDebug**：

~~~go
session := conversation.NewSession(task.ID, systemPrompt,
    prompts.ResearchCodingDebugUserPrompt(task.Description, sourceContext))
for repair := 1; repair <= 2; repair++ {
    msg, err := session.Generate(ctx, model)
    response := parse(msg.Content)
    applyPatches(...)
    runResult := runPaperCodeOnce(...)
    if runResult.ok { break }
    session.AddUser("Run failed: " + runResult.Error + "\nCurrent patch applied: ...")
}
~~~

要点：

- 初始 user 只放 task + source（稳定）；repair 反馈追加；
- 上一轮 patch 已经在 assistant message 里，不再重复贴完整 patch；
- 如果 source 文件被 patch 改变，追加“变更摘要”而不是重写整个 source；
- repair 序号只出现在 user 反馈里（如果必须），不要放 system/first user。

**BenchmarkAdapter**：

- 可以把 plan + generation + preflight repair 做成一个 session：
  ~~~text
  system: BenchmarkAdapterSystemPrompt
  user: intent + manifest + repoContext
  assistant: plan JSON
  user: "Generate adapter code for selected plan"
  assistant: adapter code
  user: preflight error
  assistant: repaired code
  ...
  ~~~
- 如果不想合并 plan/generation，至少让 generation 的 user 顺序与 plan 完全一致（intent → manifest → repoContext → plan），并在 repair 时复用同一 session。

**AutoResearch**：

- 每个 task 一个 session：
  ~~~text
  system: AutoResearchCandidateSystemPrompt
  user: spec + editable source + read-only source
  assistant: proposal 1
  user: ledger summary + evaluator feedback
  assistant: proposal 2
  ...
  ~~~
- 如果候选被 keep，editable source 变了：不要重写第一条 user，而是追加 user message：“Best candidate changed; new file hashes: ...; diff: ...”。模型可以用 diff 继续。
- ledger 只追加摘要（最近 1-2 条），不重发完整 EvalResults。

**Planner repair**：

- <code>BuildNodes</code> 创建 session：system + <code>PlannerAgentUserPrompt(intent)</code>；
- 解析出 nodes 后 <code>AddAssistant(nodesJSON)</code>；
- 验证失败 <code>AddUser(validationErr)</code>，再 <code>Generate</code>；不再调用独立的 <code>RepairNodes</code> 重建 prompt。
- 保留 <code>RepairNodes</code> 作为兼容入口，但内部复用 session。

**Coder self-correction**：

- 如果保留该路径，初始 session 应为 system + <code>CoderTaskUserPrompt(task.Description + inputs)</code>；生成 code 后 <code>AddAssistant(code)</code>；执行失败 <code>AddUser(error)</code>；再次生成。当前 <code>CoderSelfCorrectionUserPrompt</code> 只发 error 的问题一并修复。

#### 5.3.3 持久化与审计

在 <code>Task.Metadata</code> 增加：

~~~go
"repair_session": map[string]any{
    "session_id": "...",
    "turns": 3,
    "prompt_hashes": []string{...},
    "messages_path": "artifact://repair-session.json", // 可选，大对象走 blob
}
~~~

要求：

- 不把 API key、完整 base64 图片写进 session；
- 大 message 走 artifact/blob，metadata 只存引用；
- UI 可展示 repair 过程（可选，但至少 API 可读）。

#### 5.3.4 预算与裁剪

- session 的 messages 也会增长；必须和优化点 D 的 <code>PromptBudget</code> 共用：
  - 永远保留 system + first user；
  - 保留最近 N 轮 assistant/user；
  - 中间轮次压缩成摘要（“第 1 轮 patch 了 X，失败原因 Y”）；
  - 单条 tool/error message 截断到 2-4KB。

#### 5.3.5 测试与验收

单元测试：

- <code>Session.Generate</code> 后 messages 长度 +2（assistant + user）；
- 第 2 轮请求的 prefix hash 与第 1 轮相同（除新增后缀）；
- 裁剪后 system + first user 不变；
- 每个 repair 循环的 mock model 收到 assistant message，且上一轮 code/patch 只出现一次。

验收：

- PaperDebug 2 次 repair、BenchmarkAdapter 3 次 preflight、Planner 1 次 repair、AutoResearch 8 trials 均能完成；
- 第 2 轮起 provider <code>cached_tokens</code> 明显 >0（以实测为准）；
- 修复质量不下降：同一失败样本的最终 patch 与旧实现相比不增加失败率；
- <code>go test ./internal/agent ./internal/planner</code> 全绿。

---

## 6. 优化点 D：Token / context 预算

### 6.1 问题在哪

当前没有“整段 prompt 的 token 预算”，只有分散的字节/字符限制：

| 位置 | 当前限制 | 问题 |
|---|---|---|
| <code>agent/input_summary.go:14-35</code> | 每个 value 6000/12000/20000 字节 | 多 value 无总上限；中文按字节截断会切坏 UTF-8 |
| <code>scheduler/executor.go:561-566</code> | artifact 前 500 字节 | 只用于摘要，和完整内容重复；byte 截断 |
| <code>agent/autoresearch.go:41-47</code> | 8 个 editable × 96KB = 768KB；read-only 96KB；feedback 96KB | 无总预算；加 ledger 可超 1MB 字符 |
| <code>agent/autoresearch.go:1829-1834</code> | <code>recentAutoResearchTrials</code> 返回完整 <code>ResearchTrial</code>，含 <code>EvalResults</code> preview | 4 条 trial 可 100KB+ |
| <code>agent/paper_debug_harness.go:22-29</code> | 256KB context | 中文约 17 万 token，超 128k context |
| <code>agent/claim_evidence.go:20-25</code> | paper 96KB / evidence 128KB | 无 token 估算；中文可超 context |
| <code>agent/benchmark_adapter.go:30-32</code> | 32KB repo context | 相对安全，但仍是字节限制 |
| <code>modelprofile/profile.go:46-68</code> | 只有 <code>MaxOutputTokens</code>，没有 context window 字段 | 无法按模型配置预算 |

此外：

- 多处 byte slice：<code>input_summary.go:49/58/67/76</code>、<code>executor.go:561-566</code>、<code>benchmark_adapter.go:493-497</code>、<code>claim_evidence.go:694-704</code>、<code>paper_debug_harness.go:646-650</code>、<code>visual_verifier.go:685-690</code>。
- 中文 UTF-8 一个汉字 3 字节，<code>text[:limit]</code> 可能切在字符中间；JSON 序列化后变成 U+FFFD，模型看到坏字符。
- 不同 agent 的 per-value limit 不同（6000/12000/20000），同一 artifact 在不同节点被截成不同长度，破坏缓存前缀。
- 没有 output reserve：prompt 预算没有扣除 <code>max_tokens</code>，长 prompt + 长输出会触发 400。

### 6.2 为什么这么做

1. **避免 context overflow**：DeepSeek 128k context 下，256KB 中文约 17 万 token；AutoResearch 最坏 30-50 万 token；400 错误会导致整个 task 失败/重试。
2. **避免坏数据**：byte 截断切坏 UTF-8，模型看到 U+FFFD，可能误判代码/论文内容。
3. **提高缓存**：同一 section 的截断长度稳定，prefix 稳定；volatile 放最后，只截断尾部。
4. **可解释**：预算报告能告诉用户/开发者“哪些材料被丢了、为什么”，而不是静默截断。
5. **成本可控**：按 token 而不是字节，才能估算费用和延迟。

### 6.3 怎么做

#### 6.3.1 PromptBudget 与 TokenCounter

新增 <code>internal/prompts/budget.go</code>：

~~~go
type PromptBudget struct {
    ContextWindow int // 模型上下文窗口，默认 128000
    MaxOutput     int // 输出预留，默认 8192
    Safety        int // 安全余量，默认 2000
}

func (b PromptBudget) PromptLimit() int {
    if b.ContextWindow <= 0 { b.ContextWindow = 128000 }
    if b.MaxOutput <= 0 { b.MaxOutput = 8192 }
    if b.Safety <= 0 { b.Safety = 2000 }
    return b.ContextWindow - b.MaxOutput - b.Safety
}

type TokenCounter interface {
    Count(text string) int
}

// 默认实现：优先 provider tokenizer；没有时用 usage.EstimateTokens 的保守估算
type EstimatedCounter struct{}
func (EstimatedCounter) Count(text string) int { return usage.EstimateTokens(text) }
~~~

#### 6.3.2 Section 与 Builder

~~~go
type Section struct {
    Name      string
    Content   string
    Priority  int // 0 system, 1 plan, 2 task, 3 primary input, 4 secondary input, 5 volatile
    MinTokens int // 至少保留；0 表示可全丢
}

type PromptReport struct {
    TotalTokens int
    Budget      int
    Sections    []SectionReport
}

type SectionReport struct {
    Name           string
    OriginalTokens int
    IncludedTokens int
    Truncated      bool
    Dropped        bool
}

func BuildSections(sections []Section, budget PromptBudget, counter TokenCounter) (string, PromptReport, error)
~~~

规则：

1. 按 Priority 从低到高（0 最优先）分配预算；
2. system / plan / task 必须完整，若三者之和 > budget，直接返回错误（配置/数据异常，不静默截断）；
3. 其他 section 按剩余预算截断；截断必须 rune-safe；
4. 每个 section 加边界标记：
   ~~~text
   [section: paper_text]
   ...
   [truncated: 24k/96k chars]
   ~~~
5. volatile 最后，优先丢弃/截断。

#### 6.3.3 各路径的 section 策略

| 路径 | Section 顺序 | 预算建议 |
|---|---|---|
| General direct | system → plan_context → task_instruction | plan_context ≤2k tokens；task ≤2k |
| General research | system → plan_context → task → background_context | background ≤8k tokens |
| Paper parse | system → plan_context → task → retrieval chunks | retrieval ≤24k tokens |
| Claim rubric | system → plan_context → task → parsed_paper | parsed_paper ≤24k tokens |
| Claim evidence | system → plan_context → task → rubric → evidence | rubric ≤8k；evidence ≤32k |
| PaperDebug | system → task → source_context → diagnostic | source ≤48k tokens；diagnostic ≤4k |
| BenchmarkAdapter | system → intent → manifest → repo_context → plan/spec → failure/code | repo ≤8k tokens；code ≤16k |
| AutoResearch | system → spec → editable_source → read_only → ledger → feedback | editable ≤50k tokens；read_only ≤24k；ledger ≤4k；feedback ≤8k |

注意：这些是初始值，应按模型 context window 和实际 eval 校准；关键是**顺序固定 + 有总预算**。

#### 6.3.4 模型 profile 增加 context window

<code>modelprofile.Profile</code> 当前只有 <code>MaxOutputTokens</code>（<code>profile.go:61-64</code>），建议增加：

~~~go
ContextWindowTokens int `json:"context_window_tokens,omitempty"` // default 128000
~~~

<code>Input</code>、API、UI 同步；未配置时按 128000；如果 provider 返回 400 context_length，记录并提示用户调整。

#### 6.3.5 改造点

- <code>formatTaskInputsForPrompt</code> 改为 <code>formatTaskInputsForPromptBounded(inputs, budget, policy)</code>，返回 <code>(string, PromptReport)</code>。
- <code>buildTaskDescription</code> 不再截断 artifact；只输出 node-local。
- AutoResearch：
  - <code>ledgerSummary</code> 只保留最近 2 条 trial 的 <code>number/status/hypothesis/decision/reason/metric/delta</code>；
  - <code>latestEvaluatorEvidence</code> 只保留 stdout/stderr 各 2KB；
  - <code>autoResearchSourceContext</code> 按文件优先级 + 总 token 预算；
  - <code>recentAutoResearchTrials</code> 不再直接 JSON 化完整 <code>ResearchTrial</code>。
- PaperDebug：
  - <code>collectPaperDebugContext</code> 先按文件优先级/大小选择，entry file 最后；
  - <code>paperDebugMaxContextBytes</code> 改为 token 预算，保留字节上限作为硬保护。
- Claim：
  - <code>truncateClaimBytes</code> 改为 <code>truncateClaimRunes</code> 或 token-aware；
  - paper/evidence 按 token 预算，不按 byte。
- Benchmark：
  - <code>benchmarkMaxContextBytes</code> 保留硬上限，但增加 token 预算和 section 顺序。
- 所有截断统一使用 <code>truncateRunes</code> / <code>TruncateByTokens</code>，禁止裸 <code>s[:limit]</code>。

#### 6.3.6 预算报告与指标

- <code>Task.Metadata["prompt_budget"] = report</code>（或 artifact）；
- <code>TaskUsage</code> 增加 <code>PromptTokens</code> 已有，增加 <code>PromptBudgetLimit</code>、<code>PromptTruncatedSections</code>；
- metrics：
  - <code>llm_prompt_tokens_total{model}</code>
  - <code>llm_prompt_truncated_sections_total{section}</code>
  - <code>llm_prompt_over_budget_total{model}</code>
- UI/API 计划详情展示“哪些材料被截断/丢弃”。

#### 6.3.7 测试与验收

单元测试：

- <code>BuildSections</code>：优先级、预算、rune-safe、中文/英文/emoji/代码；
- 边界：system+plan+task 超预算 → 返回错误，不发送请求；
- AutoResearch：8×96KB 输入 → prompt ≤ budget，报告列出 dropped/truncated；
- PaperDebug：256KB 中文 → prompt ≤ budget，UTF-8 有效；
- Claim：96/128KB → prompt ≤ budget。

验收：

- 所有 E2E 计划不再出现 provider context_length 400；
- prompt token 在 <code>PromptLimit</code> 内；
- 截断报告可见；
- 同一计划重复运行，prompt 的 stable section 前缀一致（hash 对比）。

---

## 7. 四个优化的协同关系

建议实施顺序：

1. **B（Prompt 去重）**：先建立单一组装入口，后续 A/C/D 都依赖它。
2. **D（Token 预算）**：在 B 的 section 结构上做预算；否则 B 改完仍然可能超限。
3. **A（General fast path）**：依赖 B/D 的稳定 prompt；先做 A 也能独立收益，但最好在 B 之后。
4. **C（Repair 多轮）**：依赖 B 的 section 结构和 D 的预算；放在最后，因为它改变 agent 内部循环。

也可以按“风险/收益”拆成两个 PR：

- PR-1：B + D（prompt 组装 + 预算），覆盖所有 agent；
- PR-2：A + C（General fast path + repair 多轮），覆盖 planner 和 repair 循环。

### 7.1 阶段计划

| 阶段 | 内容 | 预估 | 主要文件 |
|---|---|---|---|
| Phase 0 | 冻结 golden set：General 10 条、paper 5 条、repair 5 条；记录当前节点数/token/耗时 | 1 天 | <code>backend/evals</code> |
| Phase 1 | B：TaskPrompt/PlanContext；executor/4 agents 改造 | 3-5 天 | <code>internal/prompts</code>、<code>scheduler/executor.go</code>、<code>agent/*.go</code> |
| Phase 2 | D：PromptBudget/TokenCounter/Section；AutoResearch/PaperDebug/Claim 接入 | 3-5 天 | <code>internal/prompts/budget.go</code>、<code>agent/*</code>、<code>modelprofile</code> |
| Phase 3 | A：GeneralPlanShape + buildGeneralNodesV2 + 灰度 | 2-3 天 | <code>planner/planner.go</code>、<code>planner/general_plan.go</code>、<code>models/graph.go</code> |
| Phase 4 | C：conversation.Session + 4 个 repair 循环接入 | 4-6 天 | <code>internal/agent/conversation</code>、<code>paper_debug_harness.go</code>、<code>benchmark_harness.go</code>、<code>autoresearch.go</code>、<code>agent_planner.go</code> |
| Phase 5 | 指标/灰度/A-B/回滚演练 | 2-3 天 | <code>metrics</code>、<code>api/observability.go</code> |

### 7.2 风险与回滚

| 风险 | 缓解 |
|---|---|
| General fast path 把复杂任务降级 | 不确定走 research；<code>GENERAL_PLAN_FAST_PATH</code> 可关闭；golden set 覆盖 under-routing |
| Prompt 去重改变模型行为 | 保留 <code>PROMPT_CONTEXT_V2</code> 开关；A/B 对比输出质量；先 shadow 记录新旧 prompt 差异 |
| 多轮 session 让上下文变长 | 与预算共用；只保留最近 N 轮；system+first user 永不裁剪 |
| 预算过紧导致关键材料被丢 | section 优先级 + 报告；system/plan/task 超预算直接报错；按 eval 校准 |
| tokenizer 与 provider 不一致 | 保留 10-15% safety；用 provider usage 回填真实 token；估算值标记 estimated |
| 旧计划/旧 API 兼容 | 新字段 omitempty；旧计划不迁移；旧 prompt 逻辑可通过开关恢复 |

---

## 8. 总验收清单

- [ ] General 简单请求：1 个节点、1 次 LLM；research 2 个节点；paper/literature 形状不变。
- [ ] 任意 task prompt 中，Global user intent 只出现一次；artifact 内容只出现一次。
- [ ] PaperDebug / BenchmarkAdapter / AutoResearch / Planner repair 的请求 messages 包含 assistant 上一轮输出；第 2 轮 prefix hash 稳定。
- [ ] 所有 prompt 满足 <code>PromptLimit</code>；中文/emoji/代码截断后仍是合法 UTF-8；预算报告可见。
- [ ] 不出现 provider context_length 400；不出现因截断导致的 JSON parse 失败增加。
- [ ] <code>go test ./...</code> 通过；关键包 <code>-race</code> 通过。
- [ ] E2E：paper reproduction 14 节点、benchmark、AutoResearch 至少各 1 条通过；计划耗时/ token 有前后对比。
- [ ] 指标可见：GeneralPlanShape、prompt tokens、truncated sections、repair turns、cache hit（与审计报告联动）。

---

## 9. 附录：关键代码位置索引

| 主题 | 文件:行 |
|---|---|
| General 3 节点 | <code>internal/planner/planner.go:1499-1537</code> |
| General 节点合并 | <code>internal/planner/agent_planner.go:167-197</code> |
| ChatAgent 作为 general_agent | <code>internal/api/routes.go:108-123</code>、<code>internal/scheduler/executor.go:81-85</code>、<code>277-281</code> |
| ChatAgent 自动代码执行 | <code>internal/agent/chat.go:99-118</code> |
| TaskDescription 已含原始意图 | <code>internal/prompts/prompts.go:805-811</code>、<code>internal/planner/planner.go:174-180</code> |
| buildTaskDescription 重复 | <code>internal/scheduler/executor.go:371-411</code> |
| Agent 再拼 inputs | <code>agent/data.go:142-145</code>、<code>librarian.go:124-127</code>、<code>answer.go:91-93</code>、<code>coder.go:470-483</code> |
| input 截断 | <code>agent/input_summary.go:14-35</code>、<code>45-76</code> |
| PaperDebug repair | <code>agent/paper_debug_harness.go:85-184</code>、<code>241-253</code>、<code>283-339</code> |
| BenchmarkAdapter repair | <code>agent/benchmark_harness.go:26-154</code>、<code>538-558</code> |
| AutoResearch candidate | <code>agent/autoresearch.go:396-742</code>、<code>1396-1408</code>、<code>1829-1834</code> |
| Planner repair | <code>planner/agent_planner.go:102-124</code> |
| Coder self-correction | <code>agent/coder_eino.go:186-191</code> |
| AutoResearch 大小限制 | <code>agent/autoresearch.go:41-47</code> |
| PaperDebug 大小限制 | <code>agent/paper_debug_harness.go:22-29</code> |
| Claim 大小限制 | <code>agent/claim_evidence.go:20-25</code>、<code>694-704</code> |
| Benchmark 大小限制 | <code>agent/benchmark_adapter.go:30-32</code>、<code>268-347</code> |
| Model profile 字段 | <code>internal/modelprofile/profile.go:46-68</code> |
| TaskUsage 字段 | <code>internal/models/runtime.go:32-43</code> |

---

## 10. 结论

这四个优化不是“锦上添花”，而是当前 agent 链路的四个结构性缺口：

- General 3 节点是**过度规划**，不是能力；
- Prompt 重复是**同一事实多次注入**，既费 token 又破坏缓存；
- Repair 无状态是**模型看不到自己上一轮输出**，质量与成本双输；
- 无 token 预算是**把 context 当成无限大**，中文/长文档下必然出问题。

先做 B+D，再做 A+C，可以用较小的改动拿到最大收益：General 简单请求 LLM 次数 3→1，prompt token 下降 10-30%，repair 循环前缀稳定，context overflow 归零，并为后续 L0-L4 复杂度路由和 cache 优化打基础。

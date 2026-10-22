# IntentClassifier

`internal/Intent/classifier.go` 把用户自然语言转换为下游 Planner 可用的 `models.IntentContext`。它做两件事：

1. **专业化重写**：在不改变语义的前提下，把表达改写得更清晰、更专业。
2. **意图识别与实体抽取**：输出意图类型、实体、约束、置信度和原因。

## 主要类型

```go
type IntentClassifier struct {
    enabled   bool
    chatModel *openai.ChatModel
}
```

```go
type IntentContext struct {
    RawIntent       string         `json:"raw_intent"`
    RewrittenIntent string         `json:"rewritten_intent,omitempty"`
    IntentType      string         `json:"intent_type"`
    Entities        map[string]any `json:"entities"`
    Constraints     map[string]any `json:"constraints"`
    Metadata        map[string]any `json:"metadata"`
    Confidence      float64        `json:"confidence,omitempty"`
    Reasoning       string         `json:"reasoning,omitempty"`
    Source          string         `json:"source,omitempty"`
}
```

意图类型包括 `Framework_Evaluation`、`Paper_Reproduction`、`Code_Execution` 和 `General`。

## 主流程

```go
func (c *IntentClassifier) Classify(ctx context.Context, rawQuery string) (models.IntentContext, error)
```

1. 检查分类器是否可用。
2. 调用 `rewriteQuery` 做专业化重写；失败则回退原始 query。
3. 构造意图识别提示词，同时携带原始 query 与重写 query。
4. 调用 LLM 并解析严格 JSON。
5. 校验意图类型，规范化 `frameworks` 等实体。
6. 返回 `IntentContext`。

## 可用性

`NewIntentClassifier()` 从环境变量读取：

- `OPENAI_API_KEY`（必填）
- `OPENAI_BASE_URL`（可选）
- `OPENAI_MODEL_NAME`（可选）

初始化失败时返回 `enabled=false` 的实例。调用方应检查 `Enabled()`，并在不可用时使用规则回退。

## 提示词设计

- 明确四类意图的边界和实体字典。
- 要求严格 JSON 输出。
- 使用 few-shot 覆盖常见研究任务。
- 重写提示词强约束“语义不变、不扩写需求、保留步骤顺序”。

## 测试

```bash
go test ./internal/Intent/...
```

测试不依赖真实 LLM 的用例使用固定响应或 mock；真实模型调用需要 `OPENAI_API_KEY`。

# tool

一个零第三方依赖的 Go 工具调用框架示例，演示如何把 LLM Function Calling 的注册、路由和执行循环串起来。

## 与主项目的关系

- [系统架构图](../docs/diagrams/architecture.drawio)（[SVG 预览](../docs/diagrams/architecture.svg)）

`tool/` 是独立的 Function Calling 教学示例，不参与 `research-assistant` 的运行时；主项目的工具/Agent 路由见 `research-assistant/backend/internal/agent` 与 `internal/tools`。

## 文件

| 文件 | 说明 |
|---|---|
| `tools.go` | `Tool` 接口、`Registry`、`Executor` 与三个示例工具 |
| `main.go` | 模拟 LLM 决策与 Agent 循环 |
| `go.mod` | 独立模块定义 |

## 设计

```text
用户输入
  └─► LLM 推理（读取 ToolDefinition JSON Schema）
        ├─ 决定调用工具
        │    └─► Executor.Execute(name, input)
        │              └─► Registry.Get(name)
        │                      ├─ search_web    -> Tavily + DuckDuckGo 聚合
        │                      ├─ deep_research -> 多轮 Tavily 融合
        │                      └─ ai_answer     -> Perplexity 直接答案
        │
        └─ 直接输出最终答案
```

## 核心接口

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error)
}
```

`ToolDefinition` 返回标准 JSON Schema，可直接作为 LLM 请求的 `tools` 参数。`Registry` 按名称路由，`Executor` 统一加 10 秒超时。

## 运行

```bash
cd tool
go run .
```

`main.go` 使用 `FakeLLM` 模拟两轮决策：第一轮调用 `search_web`，第二轮返回总结。替换 `FakeLLM` 为真实 LLM 客户端即可接入生产模型。

## 内置工具

| 工具 | 用途 | 底层实现 |
|---|---|---|
| `search_web` | 通用网络搜索 | Tavily 主、DuckDuckGo 兜底（当前为 mock） |
| `deep_research` | 多角度深度检索 | 多轮 Tavily 融合（当前为 mock） |
| `ai_answer` | 直接结论 | Perplexity（当前为 mock） |

## 扩展新工具

实现 `Tool` 接口并注册：

```go
type MyTool struct{}

func (m MyTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "my_tool",
        Description: "工具用途说明",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "query": map[string]interface{}{"type": "string"},
            },
            "required": []string{"query"},
        },
    }
}

func (m MyTool) Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error) {
    return ToolResponse{Content: "结果"}, nil
}

registry.Register(MyTool{})
```

## 边界

当前工具使用 mock 返回，不会真正访问 Tavily、DuckDuckGo 或 Perplexity。接入真实 API 时，在 `Execute` 内替换为 HTTP 请求，保持接口签名不变。

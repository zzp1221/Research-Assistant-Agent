# tool

[简体中文](README.md) | English

A zero-dependency Go tool-calling framework example that shows how to wire LLM Function Calling registration, routing, and the execution loop.

## Relationship to the main project

- [System architecture](../docs/diagrams/architecture.drawio) ([SVG preview](../docs/diagrams/architecture.svg))

`tool/` is a standalone Function Calling teaching example and does not participate in the `research-assistant` runtime; the product's tool/agent routing lives in `research-assistant/backend/internal/tools` and `internal/agent`.

`go build ./...` passes (this module has no test files).

## Files

| File | Description |
|---|---|
| `tools.go` | `Tool` interface, `Registry`, `Executor`, and three example tools |
| `main.go` | Simulated LLM decisions and the agent loop |
| `go.mod` | Standalone module definition |

## Design

```text
User input
  └─► LLM reasoning (reads ToolDefinition JSON Schema)
        ├─ decide to call a tool
        │    └─► Executor.Execute(name, input)
        │              └─► Registry.Get(name)
        │                      ├─ search_web    -> Tavily + DuckDuckGo aggregation (currently mock)
        │                      ├─ deep_research -> multi-round Tavily fusion (currently mock)
        │                      └─ ai_answer     -> Perplexity direct answer (currently mock)
        │
        └─ output the final answer directly
```

## Core interface

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error)
}
```

`ToolDefinition` returns standard JSON Schema that can be used directly as the LLM request's `tools` parameter. `Registry` routes by name; `Executor` adds a uniform 10-second timeout.

## Run

```bash
cd tool
go run .
```

`main.go` uses `FakeLLM` to simulate two rounds: the first calls `search_web`, the second returns a summary. Replace `FakeLLM` with a real LLM client to connect a production model.

## Built-in tools

| Tool | Purpose | Implementation |
|---|---|---|
| `search_web` | General web search | Tavily primary, DuckDuckGo fallback (currently mock) |
| `deep_research` | Multi-angle deep retrieval | Multi-round Tavily fusion (currently mock) |
| `ai_answer` | Direct conclusion | Perplexity (currently mock) |

## Extending with a new tool

Implement the `Tool` interface and register it:

```go
type MyTool struct{}

func (m MyTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "my_tool",
        Description: "Tool purpose",
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
    return ToolResponse{Content: "result"}, nil
}

registry.Register(MyTool{})
```

## Boundaries

The current tools return mocks and never call Tavily, DuckDuckGo, or Perplexity. To connect real APIs, replace the body of `Execute` with HTTP calls while keeping the interface unchanged.

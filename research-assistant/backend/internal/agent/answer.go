package agent

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// AnswerAgent produces a direct user-facing answer from the plan's research
// context and upstream evidence. It deliberately does not reuse DataAgent,
// whose prompt is a report generator.
type AnswerAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
	ChatModel    *openai.ChatModel
}

func NewAnswerAgent() *AnswerAgent {
	agent := &AnswerAgent{Name: "answer_agent", SystemPrompt: prompts.AnswerSystemPrompt}
	agent.initEinoChain()
	return agent
}

func (a *AnswerAgent) initEinoChain() {
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Fatalf("加载 LLM 配置失败: %v", err)
	}
	chatModel, err := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: llmCfg.BaseURL,
		APIKey:  llmCfg.APIKey,
		Model:   llmCfg.Model,
	})
	if err != nil {
		log.Fatalf("初始化回答模型失败: %v", err)
	}
	a.ChatModel = chatModel
	graph := compose.NewGraph[string, string]()
	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		return []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: input},
		}, nil
	}))
	graph.AddChatModelNode("LLM_Answer", chatModel)
	graph.AddLambdaNode("Answer_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return msg.Content, nil
	}))
	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Answer")
	graph.AddEdge("LLM_Answer", "Answer_Extractor")
	graph.AddEdge("Answer_Extractor", compose.END)
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译回答链失败: %v", err)
	}
	a.EinoChain = runnable
}

func (a *AnswerAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	_ = sharedContext
	if task == nil {
		return nil
	}
	question := strings.TrimSpace(task.Description)
	if question == "" {
		question = task.Name
	}
	if len(task.Inputs) > 0 {
		payload, err := json.Marshal(task.Inputs)
		if err == nil {
			text := string(payload)
			const maxInputChars = 30000
			if len(text) > maxInputChars {
				text = text[:maxInputChars] + "..."
			}
			question += "\n\n研究材料（只读，供回答使用）：\n" + text
		}
	}
	output, err := a.EinoChain.Invoke(ctx, question)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return err
	}
	task.Result = output
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]any{"general_response": output}
	return nil
}

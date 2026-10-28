package agent

import (
	"context"
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
	// Runtime resolves the plan owner's model profile per call.
	Runtime *dynamicChatModel
}

func NewAnswerAgent() *AnswerAgent {
	agent := &AnswerAgent{Name: "answer_agent", SystemPrompt: prompts.AnswerSystemPrompt, Runtime: newDynamicChatModel(nil)}
	agent.initEinoChain()
	return agent
}

func (a *AnswerAgent) initEinoChain() {
	var chatModel *openai.ChatModel
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Printf("[%s] LLM 配置不可用，回答链将使用运行期模型: %v", a.Name, err)
	} else {
		created, createErr := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL: llmCfg.BaseURL,
			APIKey:  llmCfg.APIKey,
			Model:   llmCfg.Model,
		})
		if createErr != nil {
			log.Printf("[%s] 初始化回答模型失败: %v", a.Name, createErr)
		} else {
			chatModel = created
			a.ChatModel = created
		}
	}
	graph := compose.NewGraph[string, string]()
	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		return []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: input},
		}, nil
	}))
	model := wrapRuntimeModel(a.Runtime, chatModel)
	if model != nil {
		graph.AddChatModelNode("LLM_Answer", wrapUsageModel(model))
	}
	graph.AddLambdaNode("Answer_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return msg.Content, nil
	}))
	graph.AddEdge(compose.START, "Prompt_Builder")
	if model != nil {
		graph.AddEdge("Prompt_Builder", "LLM_Answer")
		graph.AddEdge("LLM_Answer", "Answer_Extractor")
	}
	graph.AddEdge("Answer_Extractor", compose.END)
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Printf("[%s] 编译回答链失败: %v", a.Name, err)
		return
	}
	a.EinoChain = runnable
}

func (a *AnswerAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task == nil {
		return nil
	}
	question, promptReport, promptErr := buildAgentPromptText(sharedContext, task, prompts.AnswerSystemPrompt, "", 20000)
	if promptErr != nil {
		task.Status = models.StatusFailed
		task.Error = promptErr.Error()
		return promptErr
	}
	recordPromptReport(task, promptReport)
	if strings.TrimSpace(question) == "" {
		question = task.Name
	}
	if a.EinoChain == nil {
		task.Status = models.StatusFailed
		task.Error = "回答链未初始化"
		return nil
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

package agent

import (
	"context"
	"encoding/json"
	"log"
	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ChatAgent 负责简单的问答交互，并能识别是否需要执行代码
type ChatAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
	Coder        *CoderAgent // 注入 CoderAgent 以便在需要时执行代码
}

func NewChatAgent(coder *CoderAgent) *ChatAgent {
	agent := &ChatAgent{
		Name:         "chat_agent",
		Coder:        coder,
		SystemPrompt: prompts.ChatSystemPrompt,
	}

	agent.initEinoChain()
	return agent
}

func (a *ChatAgent) initEinoChain() {
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
		log.Fatalf("初始化聊天模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: input},
		}
		return messages, nil
	}))

	graph.AddChatModelNode("LLM_Chat", chatModel)

	graph.AddLambdaNode("Response_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Chat")
	graph.AddEdge("LLM_Chat", "Response_Extractor")
	graph.AddEdge("Response_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *ChatAgent) Answer(ctx context.Context, question string) (string, error) {
	response, err := a.EinoChain.Invoke(ctx, question)
	if err != nil {
		return "", err
	}

	// 如果识别到需要执行代码，尝试自动执行并追加结果
	if strings.Contains(response, "[CODE_EXECUTION_REQUIRED]") {
		log.Printf("[%s] 检测到代码执行需求，正在调用 CoderAgent 自动执行...", a.Name)

		// 创建一个临时任务给 CoderAgent；必须显式标记为 coder_agent +
		// code_execution，否则 CoderAgent 只会走代码生成链，不会真正在沙箱执行。
		tempTask := &models.Task{
			ID:          "chat-exec-" + question[:min(len(question), 10)],
			Name:        "Auto-Execute from Chat",
			Type:        "code_execution",
			Description: question,
			AssignedTo:  "coder_agent",
			Status:      models.StatusPending,
		}

		err := a.Coder.ExecuteTask(ctx, tempTask, make(map[string]interface{}))
		if err == nil {
			response = strings.Replace(response, "[CODE_EXECUTION_REQUIRED]", "🤖 **已自动在沙箱中运行**：\n\n```python\n"+tempTask.Code+"\n```\n\n**执行结果**：\n```text\n"+tempTask.Result+"\n```\n\n---", 1)
		} else {
			response = strings.Replace(response, "[CODE_EXECUTION_REQUIRED]", "❌ **沙箱执行失败**: "+err.Error()+"\n\n", 1)
		}
	}

	return response, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *ChatAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	question := strings.TrimSpace(task.Description)
	if question == "" {
		question = task.Name
	}
	if len(task.Inputs) > 0 {
		if payload, err := json.Marshal(task.Inputs); err == nil {
			text := string(payload)
			const maxInputChars = 12000
			if len(text) > maxInputChars {
				text = text[:maxInputChars] + "..."
			}
			question += "\n\n上游输入（只读，供回答使用）：\n" + text
		}
	}
	output, err := a.Answer(ctx, question)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return err
	}
	task.Result = output
	task.Status = models.StatusCompleted
	return nil
}

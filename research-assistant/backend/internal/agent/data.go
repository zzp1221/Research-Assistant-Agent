package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// DataAgent is responsible for analyzing execution logs, extracting metrics, and generating reports.
type DataAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
	ChatModel    *openai.ChatModel
	// Runtime resolves the plan owner's model profile on every call. When nil
	// the agent keeps using the startup ChatModel (unit tests).
	Runtime  *dynamicChatModel
	Verifier *VisualVerifier
}

type dataContextKey string

const dataSystemPromptContextKey dataContextKey = "data_system_prompt"

const dataUserPromptContextKey dataContextKey = "data_user_prompt"

func NewDataAgent() *DataAgent {
	agent := &DataAgent{
		Name:         "data_agent",
		SystemPrompt: prompts.DataSystemPrompt,
	}
	agent.initEinoChain(nil)
	return agent
}

// NewDataAgentWithRuntime builds a DataAgent whose Eino chain resolves the
// runtime model profile per request while keeping the startup model as a
// fallback when no profile store is configured.
func NewDataAgentWithRuntime(runtime *dynamicChatModel, verifier *VisualVerifier) *DataAgent {
	agent := &DataAgent{
		Name:         "data_agent",
		SystemPrompt: prompts.DataSystemPrompt,
		Runtime:      runtime,
		Verifier:     verifier,
	}
	agent.initEinoChain(nil)
	return agent
}

func (a *DataAgent) initEinoChain(fixed *openai.ChatModel) {
	chatModel := fixed
	if chatModel == nil {
		llmCfg, err := appconfig.LoadLLMConfig()
		if err != nil {
			log.Printf("[%s] LLM 配置不可用，报告链将使用运行期模型: %v", a.Name, err)
		} else {
			created, createErr := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
				BaseURL: llmCfg.BaseURL,
				APIKey:  llmCfg.APIKey,
				Model:   llmCfg.Model,
			})
			if createErr != nil {
				log.Printf("[%s] 初始化数据分析模型失败: %v", a.Name, createErr)
			} else {
				chatModel = created
				a.ChatModel = created
			}
		}
	}
	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		log.Printf("[%s] Eino 节点 [Prompt_Builder]: 正在组装报告生成提示词", a.Name)
		systemPrompt := a.SystemPrompt
		if prompt, ok := ctx.Value(dataSystemPromptContextKey).(string); ok && prompt != "" {
			systemPrompt = prompt
		}
		userPrompt := prompts.DataReportUserPrompt(input)
		if prompt, ok := ctx.Value(dataUserPromptContextKey).(string); ok && prompt != "" {
			userPrompt = prompt
		}
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: userPrompt},
		}
		return messages, nil
	}))

	model := wrapRuntimeModel(a.Runtime, chatModel)
	if model != nil {
		graph.AddChatModelNode("LLM_Generate_Report", wrapUsageModel(model))
	}

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		log.Printf("[%s] Eino 节点 [Report_Extractor]: 报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	if model != nil {
		graph.AddEdge("Prompt_Builder", "LLM_Generate_Report")
		graph.AddEdge("LLM_Generate_Report", "Report_Extractor")
	}
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Printf("[%s] 编译 Eino 链失败: %v", a.Name, err)
		return
	}

	a.EinoChain = runnable
}

func (a *DataAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)
	if task != nil && task.Type == "ablation_design" {
		return a.executeAblationDesign(ctx, task)
	}
	if task != nil && task.Type == "claim_evidence_build" {
		return a.executeClaimEvidenceBuild(ctx, task)
	}

	verification := a.runVerification(ctx, task, sharedContext)

	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := prompts.DataSystemPromptForTask(intentType, task.Type, task.Name, task.Description)
	input, promptReport, promptErr := buildAgentPromptText(sharedContext, task, systemPrompt, "", 6000)
	if promptErr != nil {
		task.Status = models.StatusFailed
		task.Error = promptErr.Error()
		return promptErr
	}
	recordPromptReport(task, promptReport)
	ctx = context.WithValue(ctx, dataSystemPromptContextKey, systemPrompt)
	if task.Type == "verify_result" {
		userPrompt := prompts.AnswerPromptInput(input)
		if verification != nil {
			userPrompt = prompts.AnswerPromptInputWithVerification(input, verification)
		}
		ctx = context.WithValue(ctx, dataUserPromptContextKey, userPrompt)
	}

	var output string
	var err error
	if a.EinoChain == nil {
		err = fmt.Errorf("data agent chain is not initialized")
	} else {
		output, err = a.EinoChain.Invoke(ctx, input)
	}
	if err != nil {
		logToContext(ctx, "[%s] 报告生成失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("报告生成失败: %v", err)
		return err
	}

	task.Result = output
	task.Status = models.StatusCompleted
	if task.Type == "verify_result" && verification != nil {
		// The structured verdict is the source of truth; the text report is
		// framed around it and can never claim a pass the verifier did not give.
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["verification"] = verification
		task.Verification = verification
	}
	logToContext(ctx, "[%s] 任务完成: %s", a.Name, task.Name)
	return nil
}

// runVerification executes the visual verification pipeline for verify_result.
// It returns nil for non-verify task types.
func (a *DataAgent) runVerification(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) *models.Verification {
	if task == nil || task.Type != "verify_result" {
		return nil
	}
	verifier := a.Verifier
	if verifier == nil {
		verifier = NewVisualVerifier(modelresolver.Default())
	}
	ownerID := metadataString(task, "tenant_id")
	if ownerID == "" {
		ownerID = modelresolver.OwnerFromContext(ctx)
	}
	intent := strings.TrimSpace(sharedContextValue(sharedContext, "user_intent"))
	if intent == "" {
		intent = metadataString(task, "user_intent")
	}
	images := ImageInputsFromTask(task.Inputs, task.ImageBase64)
	input := VerifyInput{
		Intent:  intent,
		Code:    task.Code,
		Logs:    buildVerificationLogs(task),
		Images:  images,
		OwnerID: ownerID,
	}
	verification := verifier.Verify(ctx, input)
	logToContext(ctx, "[%s] 校验完成: status=%s level=%s images=%d reason=%s", a.Name, verification.Status, verification.Level, len(verification.Images), verification.Reason)
	return &verification
}

func buildVerificationLogs(task *models.Task) string {
	if task == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if strings.TrimSpace(task.Result) != "" {
		parts = append(parts, "result:\n"+truncateToTokens(task.Result, agentEnvInt("VERIFICATION_RESULT_MAX_TOKENS", 8000)))
	}
	for _, key := range []string{"execution_result", "run_metrics", "execution_log", "logs"} {
		if value, ok := task.Inputs[key]; ok {
			encoded, err := json.Marshal(value)
			if err != nil {
				continue
			}
			if len(encoded) > 6000 {
				encoded = []byte(prompts.TruncateUTF8Bytes(string(encoded), 6000))
			}
			parts = append(parts, key+": "+string(encoded))
		}
	}
	return strings.Join(parts, "\n\n")
}

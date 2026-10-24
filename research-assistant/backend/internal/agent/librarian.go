package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// LibrarianAgent 负责文献检索、解析与总结
type LibrarianAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
	ChatModel    *openai.ChatModel
	Documents    *documents.Store
}

type librarianContextKey string

const librarianSystemPromptContextKey librarianContextKey = "librarian_system_prompt"

func NewLibrarianAgent() *LibrarianAgent {
	agent := &LibrarianAgent{
		Name:         "librarian_agent",
		SystemPrompt: prompts.LibrarianSystemPrompt,
	}

	agent.initEinoChain()
	return agent
}

func (a *LibrarianAgent) initEinoChain() {
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
		log.Fatalf("初始化文献分析模型失败: %v", err)
	}
	a.ChatModel = chatModel

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装文献分析提示词", a.Name)
		systemPrompt := a.SystemPrompt
		if prompt, ok := ctx.Value(librarianSystemPromptContextKey).(string); ok && prompt != "" {
			systemPrompt = prompt
		}
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.LibrarianAnalysisUserPrompt(input)},
		}
		return messages, nil
	}))

	// 使用支持流式的 ChatModelNode
	graph.AddChatModelNode("LLM_Analyze_Literature", chatModel)

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Report_Extractor]: 文献分析报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Analyze_Literature")
	graph.AddEdge("LLM_Analyze_Literature", "Report_Extractor")
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *LibrarianAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)
	if task != nil && task.Type == "claim_rubric_extract" {
		return a.executeClaimRubricExtraction(ctx, task)
	}

	input := task.Description
	if task != nil && len(task.Inputs) > 0 {
		input = fmt.Sprintf("%s\n\n上游输入:\n%v", task.Description, task.Inputs)
	}
	var loadedDocument *documents.Document
	if task != nil && (task.Type == "paper_parse" || task.Type == "general_research") && a.Documents != nil {
		documentID := strings.TrimSpace(fmt.Sprint(task.Inputs["document_id"]))
		if documentID != "" && documentID != "<nil>" {
			ownerID := metadataString(task, "tenant_id")
			document, err := a.Documents.GetDocument(ctx, ownerID, documentID)
			if err != nil {
				task.Status = models.StatusFailed
				task.Error = err.Error()
				return err
			}
			loadedDocument = document
			var contextBuilder strings.Builder
			if len(document.Pages) > 0 {
				contextBuilder.WriteString(fmt.Sprintf("[p.1]\n%s", document.Pages[0].Text))
			}
			chunks, searchErr := a.Documents.Search(ctx, ownerID, documentID, task.Description+" method experiment limitation conclusion innovation contribution", 10)
			if searchErr == nil {
				for _, chunk := range chunks {
					if contextBuilder.Len() > 0 {
						contextBuilder.WriteString("\n\n")
					}
					fmt.Fprintf(&contextBuilder, "[p.%d §%s]\n%s", chunk.Page, chunk.Section, chunk.Text)
				}
			}
			contextText := contextBuilder.String()
			const maxDocumentChars = 24000
			if len(contextText) > maxDocumentChars {
				contextText = contextText[:maxDocumentChars] + "\n...[truncated]"
			}
			if strings.TrimSpace(contextText) == "" {
				contextText = documents.FullText(document)
				if len(contextText) > maxDocumentChars {
					contextText = contextText[:maxDocumentChars] + "\n...[truncated]"
				}
			}
			input += "\n\n论文检索片段（带页码/章节，供分析/引用）：\n" + contextText
		}
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	ctx = context.WithValue(ctx, librarianSystemPromptContextKey, prompts.LibrarianSystemPromptForTask(intentType, task.Type, task.Name, task.Description))

	output, err := a.EinoChain.Invoke(ctx, input)
	if err != nil {
		logToContext(ctx, "[%s] 文献解析失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("文献解析失败: %v", err)
		return err
	}

	task.Result = output
	task.Status = models.StatusCompleted
	if task.Type == "paper_parse" {
		metadata := models.PaperMetadata{}
		if raw, ok := task.Inputs["paper_metadata"]; ok {
			switch value := raw.(type) {
			case map[string]any:
				encoded, _ := json.Marshal(value)
				_ = json.Unmarshal(encoded, &metadata)
			case string:
				_ = json.Unmarshal([]byte(value), &metadata)
			}
		}
		if strings.TrimSpace(metadata.Title) == "" && loadedDocument != nil {
			if extracted, err := documents.ExtractMetadata(loadedDocument); err == nil {
				metadata = extracted
			}
		}
		metadataJSON, _ := json.Marshal(metadata)
		task.StructuredData = string(metadataJSON)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = map[string]any{
			"parsed_paper":   output,
			"paper_metadata": string(metadataJSON),
			"paper_summary":  output,
		}
	}
	logToContext(ctx, "[%s] 任务完成: %s", a.Name, task.Name)
	return nil
}

package agent

import (
	"context"
	"fmt"
	"log"
	"research-assistant-backend/internal/agent/conversation"
	"research-assistant-backend/internal/agentctx"
	"strings"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/prompts"
	"research-assistant-backend/internal/sandbox"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// extractCodeFromMessage cleans a model reply and publishes the code on the
// per-task code channel. Shared by the code-only and full Eino graphs.
func extractCodeFromMessage(ctx context.Context, name string, msg *schema.Message) (string, error) {
	logToContext(ctx, "[%s] Eino 节点 [Code_Extractor]: 开始提取大模型生成的代码", name)
	code := msg.Content
	code = strings.TrimPrefix(code, "\x60\x60\x60python\n")
	code = strings.TrimPrefix(code, "\x60\x60\x60python")
	code = strings.TrimSuffix(code, "\x60\x60\x60")
	logToContext(ctx, "[%s] 成功提取到可执行代码", name)
	if codeChan, ok := agentctx.CodeChannel(ctx); ok {
		codeChan <- code
	}
	return code, nil
}

// initRealEinoChain 使用字节跳动 Eino 框架和真实的 LLM 模型编排逻辑流
func (a *CoderAgent) initRealEinoChain() {
	// 1. 初始化真实的 LLM ChatModel（同时保留运行期 profile 解析）
	var chatModel *openai.ChatModel
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Printf("[%s] LLM 配置不可用，代码链将使用运行期模型: %v", a.Name, err)
	} else {
		created, createErr := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL: llmCfg.BaseURL,
			APIKey:  llmCfg.APIKey,
			Model:   llmCfg.Model,
		})
		if createErr != nil {
			log.Printf("[%s] 初始化大模型失败: %v", a.Name, createErr)
		} else {
			chatModel = created
			a.ChatModel = created
		}
	}
	codeModel := wrapRuntimeModel(a.Runtime, chatModel)

	// 2. 构建 Eino Graph
	codeOnlyGraph := compose.NewGraph[string, string]()
	codeOnlyGraph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装代码生成提示词", a.Name)
		systemPrompt := coderSystemPromptFromContext(ctx, a.SystemPrompt)
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.CoderTaskUserPrompt(input)},
		}
		return messages, nil
	}))
	if codeModel != nil {
		codeOnlyGraph.AddChatModelNode("LLM_Generate_Code", wrapUsageModel(codeModel))
	}
	codeOnlyGraph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return extractCodeFromMessage(ctx, a.Name, msg)
	}))
	codeOnlyGraph.AddEdge(compose.START, "Prompt_Builder")
	if codeModel != nil {
		codeOnlyGraph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
		codeOnlyGraph.AddEdge("LLM_Generate_Code", "Code_Extractor")
	}
	codeOnlyGraph.AddEdge("Code_Extractor", compose.END)

	codeOnlyRunnable, err := codeOnlyGraph.Compile(context.Background())
	if err != nil {
		log.Printf("[%s] 编译 Eino CodeOnly 链失败: %v", a.Name, err)
		return
	}
	a.CodeOnlyChain = codeOnlyRunnable

	graph := compose.NewGraph[string, string]()

	// 节点 1: 提示词模板 (Prompt Template)
	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装提示词", a.Name)
		systemPrompt := coderSystemPromptFromContext(ctx, a.SystemPrompt)
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.CoderTaskUserPrompt(input)},
		}
		return messages, nil
	}))

	// 节点 2: LLM 生成代码 (调用真实的 ChatModel)
	if codeModel != nil {
		graph.AddChatModelNode("LLM_Generate_Code", wrapUsageModel(codeModel))
	}

	// 节点 3: 清洗并提取代码
	graph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return extractCodeFromMessage(ctx, a.Name, msg)
	}))

	// 节点 4: 沙箱执行与自愈循环 (Self-Correction Loop)
	graph.AddLambdaNode("Sandbox_Execute", compose.InvokableLambda(func(ctx context.Context, code string) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Sandbox_Execute]: 准备将代码送入持久化 Docker 沙箱执行", a.Name)
		if a.Sandbox == nil {
			logToContext(ctx, "[Warning] 沙箱未初始化，跳过实际执行")
			return "【由于本地未安装或未启动 Docker Desktop，跳过沙箱执行环节】\n\n大模型生成的代码如下：\n\n" + code, nil
		}

		// 获取预先创建好的长生命周期容器 ID
		containerID, ok := agentctx.ContainerID(ctx)
		if !ok || containerID == "" {
			logToContext(ctx, "[Warning] 无法获取长生命周期容器 ID，降级为单次执行模式")
			// 创建临时沙箱执行
			tempID, err := a.Sandbox.CreatePersistentSandbox(ctx, sandbox.SandboxIdentity{TaskID: "temp"}, configuredSandboxImage(), "")
			if err != nil {
				return "", fmt.Errorf("创建临时沙箱失败: %w", err)
			}
			defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

			res, err := a.Sandbox.RunPythonCode(ctx, tempID, code)
			if err != nil {
				return "", fmt.Errorf("沙箱执行失败: %w", err)
			}

			// 处理图片输出
			if len(res.Images) > 0 {
				logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
				if codeChan, ok := agentctx.CodeChannel(ctx); ok {
					for _, imgBase64 := range res.Images {
						// 通过特殊的标识符发送图片给前端
						codeChan <- "IMAGE:" + imgBase64
					}
				}
			}

			return res.Stdout + "\n" + res.Stderr, nil
		}

		maxRetries := 3
		currentCode := code
		var finalOutput string
		var correctionSession *conversation.Session

		for i := 0; i < maxRetries; i++ {
			logToContext(ctx, "[%s] 开始在持久化容器中执行代码 (第 %d/%d 次)...", a.Name, i+1, maxRetries)

			// 改进执行逻辑：直接使用 RunPythonCode 以便沙箱能够拦截并返回图表
			res, err := a.Sandbox.RunPythonCode(ctx, containerID, currentCode)

			var output string
			if res != nil {
				output = res.Stdout + "\n" + res.Stderr
				// 处理图片输出
				if len(res.Images) > 0 {
					logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
					if codeChan, ok := agentctx.CodeChannel(ctx); ok {
						for _, imgBase64 := range res.Images {
							codeChan <- "IMAGE:" + imgBase64
						}
					}
				}
			}

			if err != nil || (res != nil && res.ExitCode != 0) {
				if err == nil {
					err = fmt.Errorf("exit code: %d", res.ExitCode)
				}
				logToContext(ctx, "[%s] 代码执行失败，触发 Self-Correction 机制。错误: %v", a.Name, err)

				logToContext(ctx, "[%s] 正在调用大模型进行代码自修复...", a.Name)
				if correctionSession == nil {
					correctionSession = conversation.NewSession("coder-self-correction",
						coderSystemPromptFromContext(ctx, a.SystemPrompt),
						prompts.CoderSelfCorrectionUserPromptWithCode(currentCode, err, output))
				} else {
					correctionSession.AddUser(fmt.Sprintf("Execution still failed: %v\nOutput:\n%s", err, output))
				}
				msg, err := correctionSession.Generate(ctx, codeModel)
				if err != nil {
					logToContext(ctx, "[%s] 错误: 自修复调用大模型失败: %v", a.Name, err)
					return "", fmt.Errorf("Self-Correction 调用大模型失败: %w", err)
				}

				currentCode = strings.TrimPrefix(msg.Content, "```python\n")
				currentCode = strings.TrimPrefix(currentCode, "```python")
				currentCode = strings.TrimSuffix(currentCode, "```")

				finalOutput = fmt.Sprintf("执行失败，已尝试修复。错误日志:\n%v\n输出:\n%s", err, output)
				continue // 尝试用新代码再次运行
			}

			// 成功执行
			logToContext(ctx, "[%s] 持久化沙箱执行成功！获取到执行结果。", a.Name)
			return output, nil
		}

		logToContext(ctx, "[%s] 达到最大重试次数，任务执行失败", a.Name)
		return finalOutput + "\n\n【达到最大重试次数，任务执行失败】", fmt.Errorf("达到最大重试次数")
	}))

	// 3. 定义边 (Edges) 将节点串联起来
	graph.AddEdge(compose.START, "Prompt_Builder")
	if codeModel != nil {
		graph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
		graph.AddEdge("LLM_Generate_Code", "Code_Extractor")
		graph.AddEdge("Code_Extractor", "Sandbox_Execute")
	}
	graph.AddEdge("Sandbox_Execute", compose.END)

	// 4. 编译成 Runnable 链
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Printf("[%s] 编译 Eino 链失败: %v", a.Name, err)
		return
	}

	a.EinoChain = runnable
	log.Printf("[%s] 成功初始化基于真实大模型的 Eino 执行链", a.Name)
}

package agent

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"research-assistant-backend/internal/agentctx"
	"strings"
	"sync"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"research-assistant-backend/internal/sandbox"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
)

const (
	defaultSandboxImage  = "python:3.9-bullseye"
	defaultPipTimeoutSec = "60"
)

// Agent 接口定义了系统中自治工作者的标准行为
type Agent interface {
	// ExecuteTask 执行分配给该 Agent 的具体任务
	ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error
}

// logToContext 辅助函数，将日志同时输出到控制台和 Context 中的 Channel，以支持 SSE 流式返回
func logToContext(ctx context.Context, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Println(msg)
	if ch, ok := agentctx.LogChannel(ctx); ok {
		// 非阻塞发送
		select {
		case ch <- msg:
		default:
		}
	}
}

// CoderAgent 负责根据需求生成代码，并在沙箱中迭代测试
type CoderAgent struct {
	Name          string
	SystemPrompt  string
	Sandbox       *sandbox.SandboxClient
	ChatModel     *openai.ChatModel
	CodeOnlyChain compose.Runnable[string, string]
	EinoChain     compose.Runnable[string, string] // 使用 Eino 编排的执行链
	// Runtime resolves the plan owner's model profile per request.
	Runtime *dynamicChatModel
}

func BuildCoderSystemPrompt() string {
	return "你是一个资深的 AI 科研助理和 Python 开发者。你的任务是在 Docker 沙箱（python:3.9-bullseye 镜像）中生成可运行的 Python 代码。\n\n" +
		"【通用约束】：\n" +
		"1. 只输出纯 Python 代码，不要 Markdown 包裹。\n" +
		"2. 依赖名称必须保持通用，例如 your-package-name；不要在通用提示词中写死具体框架。\n" +
		"3. 不依赖外网 API、私有密钥或远程数据集，优先使用本地构造样例。\n" +
		"4. 运行结束后输出 JSON 格式的结果摘要，便于调度器解析。\n"
}

type coderContextKey string

const (
	coderSystemPromptContextKey coderContextKey = "coder_system_prompt"
	coderIntentTypeContextKey   coderContextKey = "coder_intent_type"
	coderTaskTypeContextKey     coderContextKey = "coder_task_type"
	coderTaskNameContextKey     coderContextKey = "coder_task_name"
)

// NewCoderAgent 实例化一个新的 CoderAgent，并初始化真实的 Eino 执行链
func NewCoderAgent(sandbox *sandbox.SandboxClient) *CoderAgent {
	agent := &CoderAgent{
		Name:         "coder_agent",
		SystemPrompt: prompts.CoderSystemPrompt,
		Sandbox:      sandbox,
		Runtime:      newDynamicChatModel(nil),
	}
	agent.initRealEinoChain()

	return agent
}

func coderSystemPromptFromContext(ctx context.Context, fallback string) string {
	if prompt, ok := ctx.Value(coderSystemPromptContextKey).(string); ok && strings.TrimSpace(prompt) != "" {
		return prompt
	}
	return fallback
}

func sharedContextValue(sharedContext map[string]interface{}, key string) string {
	if sharedContext == nil {
		return ""
	}
	value, ok := sharedContext[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (a *CoderAgent) contextWithTaskPrompt(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) context.Context {
	if task == nil {
		return ctx
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := prompts.CoderSystemPromptForTask(intentType, task.Type, task.Name, task.Description)
	ctx = context.WithValue(ctx, coderSystemPromptContextKey, systemPrompt)
	ctx = context.WithValue(ctx, coderIntentTypeContextKey, intentType)
	ctx = context.WithValue(ctx, coderTaskTypeContextKey, task.Type)
	ctx = context.WithValue(ctx, coderTaskNameContextKey, task.Name)
	return ctx
}

func deterministicFrameworkBenchmarkCode(task *models.Task) (string, bool) {
	target := frameworkBenchmarkTarget(task)
	if target == "" || task == nil || strings.TrimSpace(task.Type) != "generate_code" {
		return "", false
	}
	return buildDeterministicFrameworkBenchmarkCode(target), true
}

func frameworkBenchmarkTarget(task *models.Task) string {
	if task == nil {
		return ""
	}
	branchText := strings.ToLower(strings.Join(append([]string{task.Name}, task.OutputArtifacts...), "\n"))
	switch {
	case strings.Contains(branchText, "llamaindex") || strings.Contains(branchText, "llama_index") || strings.Contains(branchText, "llama-index"):
		return "llamaindex"
	case strings.Contains(branchText, "langchain"):
		return "langchain"
	}

	description := strings.ToLower(task.Description)
	hasLlamaIndex := strings.Contains(description, "llamaindex") || strings.Contains(description, "llama_index") || strings.Contains(description, "llama-index")
	hasLangChain := strings.Contains(description, "langchain")
	switch {
	case hasLlamaIndex && !hasLangChain:
		return "llamaindex"
	case hasLangChain && !hasLlamaIndex:
		return "langchain"
	default:
		return ""
	}
}

func buildDeterministicFrameworkBenchmarkCode(target string) string {
	packageName := "langchain"
	moduleName := "langchain"
	displayName := "LangChain"
	if target == "llamaindex" {
		packageName = "llama-index"
		moduleName = "llama_index"
		displayName = "LlamaIndex"
	}

	return fmt.Sprintf(`import hashlib
import json
import math
import statistics
import time

FRAMEWORK = %q
PACKAGE_NAME = %q
FRAMEWORK_MODULE = %q

DOCS = [
    {"id": "doc_rag", "text": "RAG systems combine retrieval over private documents with a generator that answers using grounded context."},
    {"id": "doc_eval", "text": "Offline framework benchmarks should compare indexing latency, retrieval latency, recall, throughput, and stability under identical data."},
    {"id": "doc_agent", "text": "Agent orchestration frameworks focus on tool calling, state transitions, retries, observability, and workflow recovery."},
    {"id": "doc_vector", "text": "Vector stores support similarity search over embeddings and often add metadata filters, persistence, and approximate nearest neighbor indexes."},
]

QUERIES = [
    {"query": "how should a RAG benchmark compare frameworks", "expected": "doc_eval"},
    {"query": "what does RAG combine", "expected": "doc_rag"},
    {"query": "what do agent frameworks focus on", "expected": "doc_agent"},
    {"query": "what does a vector store provide", "expected": "doc_vector"},
]

def embed(text, dim=64):
    vector = [0.0] * dim
    for token in text.lower().replace(".", " ").replace(",", " ").split():
        digest = hashlib.sha256(token.encode("utf-8")).digest()
        index = int.from_bytes(digest[:4], "big") %% dim
        sign = 1.0 if digest[4] %% 2 == 0 else -1.0
        vector[index] += sign
    norm = math.sqrt(sum(value * value for value in vector)) or 1.0
    return [value / norm for value in vector]

def cosine(left, right):
    return sum(a * b for a, b in zip(left, right))

def build_index(docs):
    start = time.perf_counter()
    index = [{"id": doc["id"], "text": doc["text"], "embedding": embed(doc["text"])} for doc in docs]
    return index, time.perf_counter() - start

def retrieve(index, query, k=3):
    query_vector = embed(query)
    scored = sorted(((cosine(query_vector, item["embedding"]), item) for item in index), key=lambda pair: pair[0], reverse=True)
    return [item for _, item in scored[:k]]

def answer(query, contexts):
    joined = " ".join(item["text"] for item in contexts)
    return "Mock answer for %%s based on %%d context chars" %% (query[:48], len(joined))

def percentile(values, ratio):
    ordered = sorted(values)
    if not ordered:
        return 0.0
    pos = min(len(ordered) - 1, int(round((len(ordered) - 1) * ratio)))
    return ordered[pos]

def main():
    version = "not_installed_offline_mock"
    index, build_seconds = build_index(DOCS)
    latencies = []
    hits = 0
    outputs = []
    for item in QUERIES:
        start = time.perf_counter()
        contexts = retrieve(index, item["query"], k=3)
        response = answer(item["query"], contexts)
        elapsed = time.perf_counter() - start
        latencies.append(elapsed)
        hits += int(any(ctx["id"] == item["expected"] for ctx in contexts))
        outputs.append({"query": item["query"], "top_ids": [ctx["id"] for ctx in contexts], "answer": response})
    total_query_seconds = sum(latencies)
    metrics = {
        "framework": FRAMEWORK,
        "package": PACKAGE_NAME,
        "framework_module": FRAMEWORK_MODULE,
        "package_version": version,
        "mode": "offline_mock_framework_benchmark",
        "offline_dependency_policy": "no external dependency installation",
        "uses_external_llm_api": False,
        "uses_external_embedding_api": False,
        "document_count": len(DOCS),
        "query_count": len(QUERIES),
        "index_build_seconds": build_seconds,
        "avg_query_latency_seconds": statistics.mean(latencies),
        "p95_query_latency_seconds": percentile(latencies, 0.95),
        "throughput_qps": len(QUERIES) / total_query_seconds if total_query_seconds else 0.0,
        "recall_at_3": hits / len(QUERIES),
        "sample_outputs": outputs,
    }
    print(json.dumps(metrics, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
`, displayName, packageName, moduleName)
}

func stripInlinePipInstall(code string) string {
	lines := strings.Split(code, "\n")
	filtered := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lower := strings.ToLower(line)
		if strings.Contains(lower, "subprocess.check_call") && strings.Contains(lower, "pip") && strings.Contains(lower, "install") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			filtered = append(filtered, indent+"pass")
			continue
		}
		// The model often writes a defensive dependency block:
		//   for pkg in (...):
		//       try:
		//           __import__(pkg)
		//       except ImportError:
		//           subprocess.check_call(...)
		// After stripping the pip line, the except block becomes empty and
		// Python raises IndentationError. Drop that whole probe block instead.
		if strings.HasSuffix(strings.TrimSpace(line), "for pkg in (\"numpy\", \"matplotlib\"):") ||
			strings.HasSuffix(strings.TrimSpace(line), "for pkg in ('numpy', 'matplotlib'):") {
			baseIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			j := i + 1
			for j < len(lines) {
				next := lines[j]
				if strings.TrimSpace(next) == "" {
					j++
					continue
				}
				nextIndent := len(next) - len(strings.TrimLeft(next, " \t"))
				if nextIndent <= baseIndent {
					break
				}
				j++
			}
			i = j - 1
			continue
		}
		filtered = append(filtered, line)
	}
	joined := strings.Join(filtered, "\n")
	// Final syntax repair for leftover empty except blocks.
	joined = strings.ReplaceAll(joined, "except ImportError:\n\n", "except ImportError:\n    pass\n\n")
	joined = strings.ReplaceAll(joined, "except Exception:\n\n", "except Exception:\n    pass\n\n")
	return joined
}

func filterFrameworkBenchmarkDependencies(task *models.Task, dependencies []string) []string {
	target := frameworkBenchmarkTarget(task)
	if target == "" {
		return dependencies
	}
	return []string{}
}

// ExecuteTask 使用 Eino Chain 执行任务
func (a *CoderAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task != nil && task.Type == "resolve_dependencies" {
		return a.resolveDependenciesTask(ctx, task)
	}
	ctx = a.contextWithTaskPrompt(ctx, task, sharedContext)
	if shouldRunDirectCodeExecution(task) && a.Sandbox != nil {
		return a.generateAndExecuteCodeTask(ctx, task, sharedContext)
	}
	// sandbox_agent tasks must run through the deterministic sandbox path so that:
	// - runtime_session / prepared_runtime artifacts are produced consistently
	// - the scheduler can wire outputs -> inputs between plan nodes
	// Routing sandbox_agent to the Eino chain can create ephemeral sandboxes and skip
	// artifact emission, which breaks dependency installation with "missing runtime_session".
	if task != nil && task.AssignedTo == "sandbox_agent" {
		return a.executeSandboxTask(ctx, task)
	}
	log.Printf("[%s] 开始执行任务: %s (使用 Eino 驱动)", a.Name, task.Name)

	codeChan := make(chan string, 10)
	ctx = context.WithValue(ctx, "codeChannel", codeChan)

	// 在后台收集生成的代码和图片
	var codeCollector sync.WaitGroup
	codeCollector.Add(1)
	go func() {
		defer codeCollector.Done()
		for msg := range codeChan {
			if strings.HasPrefix(msg, "IMAGE:") {
				task.ImageBase64 = strings.TrimPrefix(msg, "IMAGE:")
			} else {
				task.Code = msg
			}
		}
	}()

	// 通过 Eino 链运行任务描述
	selectedChain := a.CodeOnlyChain
	usesSandbox := task.AssignedTo == "sandbox_agent"
	if usesSandbox || selectedChain == nil {
		selectedChain = a.EinoChain
	}
	if usesSandbox {
		logToContext(ctx, "[%s] 当前节点为 sandbox_agent，执行时才会尝试创建或复用沙箱容器", a.Name)
	} else {
		logToContext(ctx, "[%s] 当前节点为 %s，仅执行代码生成/检查链路，不触发沙箱创建", a.Name, task.AssignedTo)
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := coderSystemPromptFromContext(ctx, prompts.CoderSystemPromptForTask(intentType, task.Type, task.Name, task.Description))
	promptText, promptReport, promptErr := buildAgentPromptText(sharedContext, task, systemPrompt, "", 12000)
	if promptErr != nil {
		close(codeChan)
		codeCollector.Wait()
		task.Status = models.StatusFailed
		task.Error = promptErr.Error()
		return promptErr
	}
	recordPromptReport(task, promptReport)
	output, err := selectedChain.Invoke(ctx, promptText)
	close(codeChan) // 关闭通道让 goroutine 退出
	codeCollector.Wait()

	if err != nil {
		log.Printf("[%s] Eino 执行流失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("执行失败: %v", err)
		return err
	}

	if replacement, ok := deterministicFrameworkBenchmarkCode(task); ok {
		output = replacement
		task.Code = replacement
	} else if task.Code != "" {
		task.Code = stripInlinePipInstall(task.Code)
		output = stripInlinePipInstall(output)
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("LOG_MODEL_OUTPUT")), "true") {
		log.Printf("[%s] Eino 执行流完成. 最终输出:\n%s", a.Name, output)
	} else {
		log.Printf("[%s] Eino 执行流完成 (output_chars=%d)", a.Name, len(output))
	}
	task.Result = output
	task.Status = models.StatusCompleted
	return nil
}

// isPlotTask reports whether a task is expected to produce a figure artifact.
func isPlotTask(task *models.Task) bool {
	if task == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.Type)) {
	case "render_plot", "result_visualization":
		return true
	default:
		return false
	}
}

// upstreamPlotImage returns a base64 PNG already produced upstream (for example
// by execute_code) so render_plot can reuse it instead of generating new code
// and rebuilding a sandbox. Non-image values are ignored.
func upstreamPlotImage(task *models.Task) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	candidates := []string{"execution_image", "plot_image", "result_plot", "image_base64"}
	for _, key := range candidates {
		value := strings.TrimSpace(extractTaskInputLike(task, key))
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "data:image/") {
			if idx := strings.Index(value, "base64,"); idx >= 0 {
				value = value[idx+len("base64,"):]
			}
		}
		if strings.HasPrefix(value, "iVBORw0KGgo") || strings.HasPrefix(value, "/9j/") {
			return value
		}
	}
	return ""
}

func (a *CoderAgent) generateAndExecuteCodeTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	// 上游已经产出图片时直接透传：不再生成代码、不再新建沙箱。
	if isPlotTask(task) {
		if image := upstreamPlotImage(task); image != "" {
			task.ImageBase64 = image
			task.Result = "reused upstream figure artifact"
			task.Status = models.StatusCompleted
			task.Error = ""
			// The image stays on task.ImageBase64 only; the scheduler externalises
			// it into the blob store, so it must not be duplicated into metadata.
			logToContext(ctx, "[%s] render plot reused upstream image artifact (no new sandbox)", a.Name)
			return nil
		}
	}
	if a.CodeOnlyChain == nil {
		task.Status = models.StatusFailed
		task.Error = "code generation chain is not configured"
		return fmt.Errorf("%s", task.Error)
	}

	logToContext(ctx, "[%s] 直接代码执行任务：先生成代码，再复用沙箱运行", a.Name)
	description := strings.TrimSpace(task.Description)
	if description == "" {
		description = task.Name
	}
	// Plot tasks must not rely on the model to re-install packages: the plan
	// already installed declared dependencies in prepared_runtime. Keep the
	// prompt explicit so the generated code only saves the figure.
	if isPlotTask(task) {
		description += "\n\n这是绘图任务：直接使用已安装好的 matplotlib/numpy，不要执行 pip install，不要调用 plt.show()。" +
			"必须把图像保存到 os.environ.get('RA_OUTPUT_DIR', '/workspace/output') 目录下（例如 os.path.join(os.environ['RA_OUTPUT_DIR'], 'result.png')），并保证生成的图片非空。"
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := coderSystemPromptFromContext(ctx, prompts.CoderSystemPromptForTask(intentType, task.Type, task.Name, task.Description))
	prompt, promptReport, promptErr := buildAgentPromptTextWithDescription(sharedContext, task, systemPrompt, description, "", 12000)
	if promptErr != nil {
		task.Status = models.StatusFailed
		task.Error = promptErr.Error()
		return promptErr
	}
	recordPromptReport(task, promptReport)
	code, err := a.CodeOnlyChain.Invoke(ctx, prompt)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("代码生成失败: %v", err)
		return err
	}
	code = stripInlinePipInstall(code)
	if strings.TrimSpace(code) == "" {
		task.Status = models.StatusFailed
		task.Error = "code generation returned empty code"
		return fmt.Errorf("%s", task.Error)
	}

	task.Code = code
	if task.Inputs == nil {
		task.Inputs = map[string]any{}
	}
	task.Inputs["generated_code"] = code
	return a.executeCodeInSandbox(ctx, task)
}

// mockLLMGenerateCode 模拟大模型根据提示词生成 Python 代码
func (a *CoderAgent) executeSandboxTask(ctx context.Context, task *models.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if a.Sandbox == nil {
		task.Status = models.StatusFailed
		task.Error = "sandbox client is not configured"
		return fmt.Errorf("%s", task.Error)
	}

	switch inferSandboxTaskKind(task) {
	case "prepare_runtime":
		return a.prepareRuntime(ctx, task)
	case "install_dependencies":
		return a.installDependencies(ctx, task)
	case "runtime_probe":
		return a.runtimeProbe(ctx, task)
	case "image_prepare":
		return a.imagePrepare(ctx, task)
	case "runtime_validate":
		return a.runtimeValidate(ctx, task)
	case "execute_code":
		return a.executeCodeInSandbox(ctx, task)
	default:
		return a.executeCodeInSandbox(ctx, task)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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
	// Literature is the provider client used by literature_search tasks.
	Literature *literature.Client
	// Library stores literature records discovered during a search.
	Library *literature.LibraryStore
	// Runtime resolves the plan owner's model profile per request.
	Runtime *dynamicChatModel
}

type librarianContextKey string

const librarianSystemPromptContextKey librarianContextKey = "librarian_system_prompt"

func NewLibrarianAgent() *LibrarianAgent {
	agent := &LibrarianAgent{
		Name:         "librarian_agent",
		SystemPrompt: prompts.LibrarianSystemPrompt,
		Runtime:      newDynamicChatModel(nil),
	}

	agent.initEinoChain()
	return agent
}

func (a *LibrarianAgent) initEinoChain() {
	var chatModel *openai.ChatModel
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Printf("[%s] LLM 配置不可用，文献链将使用运行期模型: %v", a.Name, err)
	} else {
		created, createErr := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL: llmCfg.BaseURL,
			APIKey:  llmCfg.APIKey,
			Model:   llmCfg.Model,
		})
		if createErr != nil {
			log.Printf("[%s] 初始化文献分析模型失败: %v", a.Name, createErr)
		} else {
			chatModel = created
			a.ChatModel = created
		}
	}

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
	model := wrapRuntimeModel(a.Runtime, chatModel)
	if model != nil {
		graph.AddChatModelNode("LLM_Analyze_Literature", wrapUsageModel(model))
	}

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Report_Extractor]: 文献分析报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	if model != nil {
		graph.AddEdge("Prompt_Builder", "LLM_Analyze_Literature")
		graph.AddEdge("LLM_Analyze_Literature", "Report_Extractor")
	}
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Printf("[%s] 编译 Eino 链失败: %v", a.Name, err)
		return
	}

	a.EinoChain = runnable
}

func (a *LibrarianAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)
	if task != nil && task.Type == "claim_rubric_extract" {
		return a.executeClaimRubricExtraction(ctx, task)
	}
	if task != nil && task.Type == "literature_search" {
		return a.executeLiteratureSearch(ctx, task)
	}

	volatile := ""
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
			// Two-stage retrieval: outline first, then BM25 inside the most
			// relevant sections. The trace line is forwarded to the task log
			// stream so retrieval quality is observable per task.
			retrieval := a.retrieveDocumentContext(ctx, ownerID, documentID, task.Description, document)
			logToContext(ctx, "%s", retrieval.traceLine())
			volatile = "论文检索片段（带页码/章节，供分析/引用）：\n" + retrieval.Context
		}
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := prompts.LibrarianSystemPromptForTask(intentType, task.Type, task.Name, task.Description)
	input, promptReport, promptErr := buildAgentPromptText(sharedContext, task, systemPrompt, volatile, 6000)
	if promptErr != nil {
		task.Status = models.StatusFailed
		task.Error = promptErr.Error()
		return promptErr
	}
	recordPromptReport(task, promptReport)
	ctx = context.WithValue(ctx, librarianSystemPromptContextKey, systemPrompt)

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

const (
	librarianSectionLimit = 6
	librarianSectionTopK  = 4
	librarianGlobalTopK   = 10
	librarianTruncation   = "\n...[truncated]"
)

// librarianRetrieval is the outcome of one two-stage retrieval run.
type librarianRetrieval struct {
	Context  string
	Sections []string
	Chunks   int
	Queries  int
}

// traceLine renders the single-line retrieval trace appended to task logs.
func (r librarianRetrieval) traceLine() string {
	return fmt.Sprintf("[retrieval] sections=%d chunks=%d queries=%d sections_list=%q",
		len(r.Sections), r.Chunks, r.Queries, strings.Join(r.Sections, " | "))
}

// retrieveDocumentContext implements two-stage retrieval for one paper:
//
//  1. Outline the document and keep up to librarianSectionLimit sections that
//     match the task description (method/experiment/result headings win).
//  2. Run BM25 SearchWithOptions restricted to each selected section and merge
//     the scored chunks, preserving page/section provenance.
//
// When the document has no recognizable sections, or no section search
// returns anything, it falls back to document-wide BM25 with diversified
// queries. The merged context is capped at documents.MaxExcerptChars runes.
func (a *LibrarianAgent) retrieveDocumentContext(ctx context.Context, ownerID, documentID, description string, document *documents.Document) librarianRetrieval {
	result := librarianRetrieval{}
	if a == nil || a.Documents == nil || document == nil || strings.TrimSpace(documentID) == "" {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}

	queries := librarianRetrievalQueries(description)
	sections := selectLibrarianSections(documents.Outline(document), description, librarianSectionLimit)
	for _, section := range sections {
		result.Sections = append(result.Sections, section.Title)
	}

	merged := make([]documents.Chunk, 0, 32)
	seen := make(map[string]struct{}, 32)
	for _, section := range sections {
		if ctx.Err() != nil {
			break
		}
		query := strings.TrimSpace(section.Title + " " + queries[0])
		chunks, err := a.Documents.SearchWithOptions(ctx, ownerID, documentID, query, documents.SearchOptions{
			TopK:    librarianSectionTopK,
			Section: section.Title,
		})
		result.Queries++
		if err != nil {
			continue
		}
		merged = appendUniqueChunks(merged, seen, chunks)
	}

	if len(merged) == 0 {
		// No outline or no section hits: search the whole document with every
		// query variant so 50+ page papers still surface method/result text.
		for _, query := range queries {
			if ctx.Err() != nil {
				break
			}
			chunks, err := a.Documents.SearchWithOptions(ctx, ownerID, documentID, query, documents.SearchOptions{
				TopK: librarianGlobalTopK,
			})
			result.Queries++
			if err != nil {
				continue
			}
			merged = appendUniqueChunks(merged, seen, chunks)
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		if merged[i].Page != merged[j].Page {
			return merged[i].Page < merged[j].Page
		}
		return merged[i].ID < merged[j].ID
	})

	result.Context, result.Chunks = buildRetrievalContext(merged, document, documents.MaxExcerptChars)
	return result
}

// librarianRetrievalQueries returns deterministic query variants covering the
// vocabulary used by academic papers in English and Chinese.
func librarianRetrievalQueries(description string) []string {
	base := strings.TrimSpace(description)
	queries := make([]string, 0, 4)
	add := func(query string) {
		query = strings.Join(strings.Fields(query), " ")
		if query == "" {
			return
		}
		for _, existing := range queries {
			if existing == query {
				return
			}
		}
		queries = append(queries, query)
	}
	add(base + " method methodology experiment evaluation result discussion limitation conclusion contribution")
	add(base + " abstract introduction background related work future work")
	add(base + " 方法 实验 结果 讨论 结论 局限 贡献")
	if base != "" {
		add(base)
	}
	if len(queries) == 0 {
		add("abstract introduction method experiment result conclusion")
	}
	return queries
}

type librarianSectionKeyword struct {
	keyword string
	weight  float64
}

var librarianSectionKeywords = []librarianSectionKeyword{
	{"abstract", 6}, {"摘要", 8},
	{"introduction", 7}, {"引言", 7}, {"绪论", 7},
	{"related work", 5}, {"background", 4}, {"相关工作", 5}, {"背景", 4},
	{"method", 10}, {"methodology", 10}, {"approach", 9}, {"方法", 10},
	{"experiment", 10}, {"evaluation", 9}, {"实验", 10}, {"评估", 8},
	{"result", 9}, {"结果", 9},
	{"discussion", 7}, {"讨论", 7},
	{"limitation", 8}, {"局限", 8},
	{"conclusion", 9}, {"结论", 9},
	{"future work", 4}, {"展望", 4},
	{"appendix", 2}, {"附录", 2},
}

// selectLibrarianSections ranks outline sections against the task description
// and keeps at most limit of them, breaking score ties by page order.
func selectLibrarianSections(sections []documents.SectionInfo, description string, limit int) []documents.SectionInfo {
	if limit <= 0 || len(sections) == 0 {
		return nil
	}
	type rankedSection struct {
		section documents.SectionInfo
		score   float64
	}
	ranked := make([]rankedSection, 0, len(sections))
	for _, section := range sections {
		score := librarianSectionScore(section.Title, description)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, rankedSection{section: section, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].section.StartPage < ranked[j].section.StartPage
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	selected := make([]documents.SectionInfo, len(ranked))
	for index := range ranked {
		selected[index] = ranked[index].section
	}
	return selected
}

// librarianSectionScore rewards canonical paper sections and direct overlap
// between the heading and the task description (including Chinese bigrams).
func librarianSectionScore(title, description string) float64 {
	lowerTitle := strings.ToLower(strings.TrimSpace(title))
	if lowerTitle == "" {
		return 0
	}
	score := 0.0
	for _, entry := range librarianSectionKeywords {
		if strings.Contains(lowerTitle, entry.keyword) {
			score += entry.weight
		}
	}
	lowerDescription := strings.ToLower(description)
	for _, term := range strings.Fields(lowerDescription) {
		term = strings.Trim(term, ".,;:!?()[]{}<>\"'`")
		if len([]rune(term)) < 3 {
			continue
		}
		if strings.Contains(lowerTitle, term) {
			score += 2
		}
	}
	seen := make(map[string]struct{}, 16)
	runes := []rune(lowerDescription)
	for index := 0; index+1 < len(runes); index++ {
		if !unicode.Is(unicode.Han, runes[index]) || !unicode.Is(unicode.Han, runes[index+1]) {
			continue
		}
		bigram := string(runes[index : index+2])
		if _, ok := seen[bigram]; ok {
			continue
		}
		seen[bigram] = struct{}{}
		if strings.Contains(lowerTitle, bigram) {
			score += 1.5
		}
	}
	return score
}

func appendUniqueChunks(target []documents.Chunk, seen map[string]struct{}, chunks []documents.Chunk) []documents.Chunk {
	for _, chunk := range chunks {
		key := chunk.ID
		if key == "" {
			key = fmt.Sprintf("%d|%s|%s", chunk.Page, chunk.Section, chunk.Text)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		target = append(target, chunk)
	}
	return target
}

// buildRetrievalContext renders merged chunks with page/section tags inside a
// rune budget. It returns the number of chunks that made it into the context.
func buildRetrievalContext(chunks []documents.Chunk, document *documents.Document, budget int) (string, int) {
	if budget <= 0 {
		return "", 0
	}
	builder := strings.Builder{}
	characters := 0
	included := 0
	for _, chunk := range chunks {
		entry := formatRetrievedChunk(chunk)
		if builder.Len() > 0 {
			entry = "\n\n" + entry
		}
		entryCharacters := utf8.RuneCountInString(entry)
		if characters+entryCharacters <= budget {
			builder.WriteString(entry)
			characters += entryCharacters
			included++
			continue
		}
		if included == 0 {
			truncated := truncateRunes(entry, budget)
			builder.WriteString(truncated)
			characters = utf8.RuneCountInString(truncated)
			included = 1
		}
		break
	}
	if builder.Len() > 0 {
		return builder.String(), included
	}
	if document == nil {
		return "", 0
	}
	return truncateRunes(documents.FullText(document), budget), 0
}

func formatRetrievedChunk(chunk documents.Chunk) string {
	section := strings.TrimSpace(chunk.Section)
	if section == "" {
		return fmt.Sprintf("[p.%d]\n%s", chunk.Page, chunk.Text)
	}
	return fmt.Sprintf("[p.%d §%s]\n%s", chunk.Page, section, chunk.Text)
}

// truncateRunes hard-caps text at limit runes without splitting UTF-8.
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= len(librarianTruncation) {
		return string(runes[:limit])
	}
	return string(runes[:limit-len(librarianTruncation)]) + librarianTruncation
}

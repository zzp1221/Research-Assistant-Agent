package Intent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/sync/errgroup"
)

// IntentClassifier 基于大模型的意图识别器
type IntentClassifier struct {
	enabled     bool
	chatModel   *openai.ChatModel
	memoryStore MemoryStore
}

// MemoryTurn 表示一轮历史对话（记忆结构体，先模拟）
type MemoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PromptMemory 表示传给大模型的上下文记忆（记忆结构体，先模拟）
type PromptMemory struct {
	SessionID   string         `json:"session_id"`
	RecentTurns []MemoryTurn   `json:"recent_turns"`
	UserProfile map[string]any `json:"user_profile"`
	Preferences map[string]any `json:"preferences"`
	TopicHints  []string       `json:"topic_hints"`
}

// llmClassifyResponse 是 LLM 返回的 JSON 结构
type llmClassifyResponse struct {
	IntentType  string         `json:"intent_type"`
	Entities    map[string]any `json:"entities"`
	Constraints map[string]any `json:"constraints"`
	Confidence  float64        `json:"confidence"`
	Reasoning   string         `json:"reasoning"`
}

// llmRewriteResponse 是 Query 重写结果的 JSON 结构
type llmRewriteResponse struct {
	RewrittenQuery string `json:"rewritten_query"`
}

// llmPaperSearchResponse 是论文仓库检索字段抽取结果。
// 这些字段用于后续 Papers with Code / GitHub 检索，避免再从长文本中二次猜测。
type llmPaperSearchResponse struct {
	PaperTitle  string  `json:"paper_title"`
	ArxivID     string  `json:"paper_arxiv_id"`
	SearchQuery string  `json:"paper_search_query"`
	MethodName  string  `json:"paper_method_name"`
	Confidence  float64 `json:"confidence"`
	Reasoning   string  `json:"reasoning"`
}

// NewIntentClassifier 创建新的意图识别器
func NewIntentClassifier() *IntentClassifier {
	apiKey := os.Getenv("OPENAI_API_KEY")
	memoryStore, memoryErr := NewRedisMemoryStoreFromEnv()
	if memoryErr != nil {
		log.Printf("[IntentClassifier] redis memory store init failed: %v", memoryErr)
		memoryStore = &NoopMemoryStore{}
	}
	if strings.TrimSpace(apiKey) == "" {
		log.Printf("[IntentClassifier] OPENAI_API_KEY not set, classifier disabled")
		return &IntentClassifier{enabled: false, memoryStore: memoryStore}
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
	})
	if err != nil {
		log.Printf("[IntentClassifier] init failed: %v, classifier disabled", err)
		return &IntentClassifier{enabled: false, memoryStore: memoryStore}
	}

	log.Printf("[IntentClassifier] initialized successfully with model=%s", modelName)
	return &IntentClassifier{
		enabled:     true,
		chatModel:   chatModel,
		memoryStore: memoryStore,
	}
}

// Enabled 返回分类器是否可用
func (c *IntentClassifier) Enabled() bool {
	return c != nil && c.enabled && c.chatModel != nil
}

// Classify 使用大模型做意图识别，并并行完成 query 重写与短期记忆注入。
func (c *IntentClassifier) Classify(ctx context.Context, userID, sessionID, rawQuery string) (models.IntentContext, error) {
	if !c.Enabled() {
		return models.IntentContext{}, fmt.Errorf("intent classifier is disabled")
	}

	memory, err := c.loadPromptMemory(ctx, userID, sessionID)
	if err != nil {
		log.Printf("[IntentClassifier] load prompt memory failed, fallback to empty memory: %v", err)
		memory = &PromptMemory{
			SessionID:   sessionID,
			RecentTurns: nil,
			UserProfile: map[string]any{},
			Preferences: map[string]any{},
			TopicHints:  nil,
		}
	}

	intentCtx, err := c.classifyRewriteAndExtractParallel(ctx, rawQuery, memory)
	if err != nil {
		return models.IntentContext{}, err
	}
	if intentCtx.Metadata == nil {
		intentCtx.Metadata = map[string]any{}
	}
	intentCtx.Metadata["normalized_intent"] = strings.ToLower(rawQuery)
	intentCtx.Metadata["session_id"] = sessionID
	intentCtx.Metadata["user_id"] = userID
	if strings.TrimSpace(intentCtx.RewrittenIntent) != "" {
		intentCtx.Metadata["rewritten_intent"] = intentCtx.RewrittenIntent
	}

	return intentCtx, nil
}

// Rewrite 将用户查询重写为更专业的表达（语义保持不变）。
func (c *IntentClassifier) Rewrite(ctx context.Context, rawQuery string, memory *PromptMemory) (string, error) {
	userPrompt := buildRewriteUserPrompt(rawQuery, memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.IntentRewriteSystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return "", fmt.Errorf("LLM query rewrite failed: %w", err)
	}

	result, err := parseRewriteResponse(msg.Content)
	if err != nil {
		return "", fmt.Errorf("failed to parse rewrite response: %w", err)
	}

	rewritten := strings.TrimSpace(result.RewrittenQuery)
	if rewritten == "" {
		return "", fmt.Errorf("rewritten_query is empty")
	}
	return rewritten, nil
}

// ExtractPaperSearchFields 只负责提取论文仓库检索所需的结构化字段。
func (c *IntentClassifier) ExtractPaperSearchFields(ctx context.Context, rawQuery string, memory *PromptMemory) (map[string]any, error) {
	userPrompt := buildPaperSearchUserPrompt(rawQuery, memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.PaperSearchSystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM paper search extraction failed: %w", err)
	}

	result, err := parsePaperSearchResponse(msg.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse paper search response: %w", err)
	}

	fields := map[string]any{}
	if title := strings.TrimSpace(result.PaperTitle); title != "" {
		fields["paper_title"] = title
	}
	if arxivID := strings.TrimSpace(result.ArxivID); arxivID != "" {
		fields["paper_arxiv_id"] = arxivID
	}
	if query := strings.TrimSpace(result.SearchQuery); query != "" {
		fields["paper_search_query"] = query
	}
	if method := strings.TrimSpace(result.MethodName); method != "" {
		fields["paper_method_name"] = method
	}
	if result.Confidence > 0 {
		fields["paper_search_confidence"] = clampConfidence(result.Confidence)
	}
	if reasoning := strings.TrimSpace(result.Reasoning); reasoning != "" {
		fields["paper_search_reasoning"] = reasoning
	}
	return fields, nil
}

// ClassifyOnly 只做分类和实体抽取，便于和 Rewrite 并行执行。
func (c *IntentClassifier) ClassifyOnly(ctx context.Context, rawQuery string, memory *PromptMemory) (models.IntentContext, error) {
	userPrompt := buildClassifyUserPrompt(rawQuery, "", memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.IntentClassificationSystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return models.IntentContext{}, fmt.Errorf("LLM intent classification failed: %w", err)
	}

	result, err := parseLLMResponse(msg.Content)
	if err != nil {
		return models.IntentContext{}, fmt.Errorf("failed to parse LLM response: %w (raw: %s)", err, truncate(msg.Content, 200))
	}
	if !isValidIntentType(result.IntentType) {
		return models.IntentContext{}, fmt.Errorf("LLM returned invalid intent_type: %q", result.IntentType)
	}

	intentCtx := models.IntentContext{
		RawIntent:   rawQuery,
		IntentType:  result.IntentType,
		Entities:    normalizeEntities(result.Entities),
		Constraints: result.Constraints,
		Confidence:  clampConfidence(result.Confidence),
		Reasoning:   result.Reasoning,
		Source:      "llm",
	}
	if intentCtx.Entities == nil {
		intentCtx.Entities = map[string]any{}
	}
	if intentCtx.Constraints == nil {
		intentCtx.Constraints = map[string]any{}
	}
	return intentCtx, nil
}

func (c *IntentClassifier) classifyRewriteAndExtractParallel(ctx context.Context, rawQuery string, memory *PromptMemory) (models.IntentContext, error) {
	var (
		intentCtx      models.IntentContext
		rewrittenQuery string
		rewriteErr     error
		paperFields    map[string]any
		paperFieldErr  error
	)

	g, groupCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		intentCtx, err = c.ClassifyOnly(groupCtx, rawQuery, memory)
		return err
	})
	g.Go(func() error {
		var err error
		rewrittenQuery, err = c.Rewrite(groupCtx, rawQuery, memory)
		if err != nil {
			rewriteErr = err
			return nil
		}
		return nil
	})
	g.Go(func() error {
		var err error
		paperFields, err = c.ExtractPaperSearchFields(groupCtx, rawQuery, memory)
		if err != nil {
			paperFieldErr = err
			return nil
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return models.IntentContext{}, err
	}

	if strings.TrimSpace(rewrittenQuery) == "" {
		rewrittenQuery = rawQuery
	}
	intentCtx.RewrittenIntent = rewrittenQuery
	if intentCtx.Metadata == nil {
		intentCtx.Metadata = map[string]any{}
	}
	if rewriteErr != nil {
		intentCtx.Metadata["rewrite_error"] = rewriteErr.Error()
		log.Printf("[IntentClassifier] query rewrite failed, fallback to raw query: %v", rewriteErr)
	}
	if paperFieldErr != nil {
		intentCtx.Metadata["paper_search_error"] = paperFieldErr.Error()
		log.Printf("[IntentClassifier] paper search field extraction failed: %v", paperFieldErr)
	}
	if len(paperFields) > 0 {
		mergePaperSearchFields(intentCtx.Entities, paperFields)
		intentCtx.Metadata["paper_search_fields"] = cloneAnyMap(paperFields)
	}

	log.Printf("[IntentClassifier] intent_type=%s confidence=%.2f source=%s",
		intentCtx.IntentType, intentCtx.Confidence, intentCtx.Source)

	return intentCtx, nil
}

func (c *IntentClassifier) loadPromptMemory(ctx context.Context, userID, sessionID string) (*PromptMemory, error) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return &PromptMemory{
			SessionID:   sessionID,
			RecentTurns: nil,
			UserProfile: map[string]any{},
			Preferences: map[string]any{},
			TopicHints:  nil,
		}, nil
	}

	ttl := sessionTTLFromEnv()
	if err := c.memoryStore.EnsureSession(ctx, userID, sessionID, ttl); err != nil {
		return nil, err
	}

	turns, err := c.memoryStore.LoadRecentTurns(ctx, sessionID, turnsFetchFromEnv())
	if err != nil {
		return nil, err
	}

	recent := make([]MemoryTurn, 0, len(turns))
	for _, turn := range turns {
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		recent = append(recent, turn.toMemoryTurn())
	}

	return &PromptMemory{
		SessionID:   sessionID,
		RecentTurns: recent,
		UserProfile: map[string]any{
			"user_id": userID,
		},
		Preferences: map[string]any{},
		TopicHints:  nil,
	}, nil
}

func (c *IntentClassifier) persistTurnsAsync(ctx context.Context, userID, sessionID, rawQuery string, intentCtx models.IntentContext) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(ctx, llmTimeoutFromEnv())
		defer cancel()

		ttl := sessionTTLFromEnv()
		maxTurns := turnsMaxFromEnv()
		if err := c.memoryStore.EnsureSession(writeCtx, userID, sessionID, ttl); err != nil {
			log.Printf("[IntentClassifier] ensure redis session failed: %v", err)
			return
		}
		if err := c.memoryStore.AppendTurn(writeCtx, sessionID, StoredTurn{
			Role:    "user",
			Content: rawQuery,
		}, maxTurns, ttl); err != nil {
			log.Printf("[IntentClassifier] append user turn failed: %v", err)
			return
		}
		if strings.TrimSpace(intentCtx.RewrittenIntent) != "" {
			if err := c.memoryStore.AppendTurn(writeCtx, sessionID, StoredTurn{
				Role:       "assistant",
				Content:    intentCtx.RewrittenIntent,
				IntentType: intentCtx.IntentType,
				Entities:   intentCtx.Entities,
			}, maxTurns, ttl); err != nil {
				log.Printf("[IntentClassifier] append assistant turn failed: %v", err)
			}
		}
	}()
}

// RecordTurn 用于在 chat 链路写入真实消息记录，供短期记忆复用。
func (c *IntentClassifier) RecordTurn(ctx context.Context, userID, sessionID string, turn StoredTurn) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return
	}

	writeCtx, cancel := context.WithTimeout(ctx, llmTimeoutFromEnv())
	defer cancel()

	ttl := sessionTTLFromEnv()
	if err := c.memoryStore.EnsureSession(writeCtx, userID, sessionID, ttl); err != nil {
		log.Printf("[IntentClassifier] ensure redis session failed: %v", err)
		return
	}
	if err := c.memoryStore.AppendTurn(writeCtx, sessionID, turn, turnsMaxFromEnv(), ttl); err != nil {
		log.Printf("[IntentClassifier] append turn failed: %v", err)
	}
}

// parseLLMResponse 解析 LLM 返回的 JSON
func parseLLMResponse(raw string) (*llmClassifyResponse, error) {
	cleaned := strings.TrimSpace(raw)
	// 去除可能的 markdown 包裹
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	// 尝试从文本中提取 JSON 对象
	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > idx {
			cleaned = cleaned[idx : end+1]
		}
	}

	var result llmClassifyResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	if result.IntentType == "" {
		return nil, fmt.Errorf("intent_type is empty in LLM response")
	}

	return &result, nil
}

// parseRewriteResponse 解析 Query 重写 JSON
func parseRewriteResponse(raw string) (*llmRewriteResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > idx {
			cleaned = cleaned[idx : end+1]
		}
	}

	var result llmRewriteResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return &result, nil
}

func parsePaperSearchResponse(raw string) (*llmPaperSearchResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > idx {
			cleaned = cleaned[idx : end+1]
		}
	}

	var result llmPaperSearchResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}
	return &result, nil
}

// isValidIntentType 检查意图类型是否合法
func isValidIntentType(intentType string) bool {
	switch intentType {
	case "Framework_Evaluation", "Paper_Reproduction", "Code_Execution", "General":
		return true
	default:
		return false
	}
}

// normalizeEntities 规范化实体字段
func normalizeEntities(entities map[string]any) map[string]any {
	if entities == nil {
		return map[string]any{}
	}

	// 规范化 frameworks：确保是 []string 类型
	if raw, ok := entities["frameworks"]; ok {
		switch v := raw.(type) {
		case []any:
			frameworks := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					frameworks = append(frameworks, strings.ToLower(strings.TrimSpace(s)))
				}
			}
			entities["frameworks"] = frameworks
			if _, hasCount := entities["framework_count"]; !hasCount {
				entities["framework_count"] = len(frameworks)
			}
		case []string:
			for i, s := range v {
				v[i] = strings.ToLower(strings.TrimSpace(s))
			}
			entities["frameworks"] = v
		}
	}

	return entities
}

func mergePaperSearchFields(dst map[string]any, fields map[string]any) {
	if dst == nil || len(fields) == 0 {
		return
	}
	for _, key := range []string{"paper_title", "paper_arxiv_id", "paper_search_query", "paper_method_name"} {
		value, ok := fields[key]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if existing, exists := dst[key]; exists && strings.TrimSpace(fmt.Sprint(existing)) != "" {
			continue
		}
		dst[key] = value
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatMemoryForPrompt(memory *PromptMemory) string {
	if memory == nil {
		return "{}"
	}
	b, err := json.Marshal(memory)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func buildClassifyUserPrompt(rawQuery, rewrittenQuery string, memory *PromptMemory) string {
	memoryJSON := formatMemoryForPrompt(memory)
	if strings.TrimSpace(rewrittenQuery) == "" {
		return prompts.IntentClassificationUserPrompt(rawQuery, memoryJSON)
	}
	return prompts.IntentClassificationWithRewriteUserPrompt(rawQuery, rewrittenQuery, memoryJSON)
}

func clampConfidence(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func sessionTTLFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("INTENT_SESSION_TTL")); raw != "" {
		if ttl, err := time.ParseDuration(raw); err == nil && ttl > 0 {
			return ttl
		}
	}
	return 7 * 24 * time.Hour
}

func turnsFetchFromEnv() int {
	return envIntWithDefault("INTENT_TURNS_FETCH", 10)
}

func turnsMaxFromEnv() int {
	return envIntWithDefault("INTENT_TURNS_MAX", 30)
}

func llmTimeoutFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("INTENT_LLM_TIMEOUT")); raw != "" {
		if timeout, err := time.ParseDuration(raw); err == nil && timeout > 0 {
			return timeout
		}
	}
	return 5 * time.Second
}

func envIntWithDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func buildRewriteUserPrompt(rawQuery string, memory *PromptMemory) string {
	return prompts.IntentRewriteUserPrompt(rawQuery, formatMemoryForPrompt(memory))
}

func buildPaperSearchUserPrompt(rawQuery string, memory *PromptMemory) string {
	return prompts.PaperSearchUserPrompt(rawQuery, formatMemoryForPrompt(memory))
}

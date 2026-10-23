package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"research-assistant-backend/internal/agent"
	intentpkg "research-assistant-backend/internal/intent"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/sandbox"
	"research-assistant-backend/internal/scheduler"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type RequestPayload struct {
	Intent      string   `json:"intent" binding:"required"`
	Attachments []string `json:"attachments,omitempty"`
}

type ChatPayload struct {
	Message string `json:"message" binding:"required"`
}

var (
	routePaperArxivIDRe       = regexp.MustCompile(`\b\d{4}\.\d{4,5}\b`)
	routeGitHubRepoURLRe      = regexp.MustCompile(`https?://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:\.git)?`)
	routeRepositoryRevisionRe = regexp.MustCompile(`(?i)(?:commit|revision|提交|版本)\s*(?:[:：=]|为|是)?\s*([a-f0-9]{64}|[a-f0-9]{40})\b`)
)

// CORSMiddleware allows frontend to communicate with backend
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && corsOriginAllowed(origin) {
			c.Writer.Header().Set("Vary", "Origin")
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-User-Id, X-Session-Id")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			if origin != "" && !corsOriginAllowed(origin) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func corsOriginAllowed(origin string) bool {
	configured := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if configured == "" {
		configured = "http://localhost:5173,http://127.0.0.1:5173"
	}
	for _, candidate := range strings.Split(configured, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == origin {
			return true
		}
	}
	return false
}

func SetupRoutes(r *gin.Engine) {
	// Apply CORS
	r.Use(CORSMiddleware())

	p := planner.NewPlanner()

	// Initialize Agents
	sandboxURL := os.Getenv("SANDBOX_URL")
	if sandboxURL == "" {
		sandboxURL = "http://localhost:8082"
	}
	sb := sandbox.NewSandboxClient(sandboxURL)
	coderAgent := agent.NewCoderAgent(sb)
	researchCodingAgent := agent.NewResearchCodingAgent(coderAgent)
	benchmarkAgent := agent.NewBenchmarkAgent()
	optimizerURL := strings.TrimSpace(os.Getenv("RESEARCH_OPTIMIZER_URL"))
	librarianAgent := agent.NewLibrarianAgent()
	dataAgent := agent.NewDataAgent()
	chatAgent := agent.NewChatAgent(coderAgent)

	apiGroup := r.Group("/api")
	apiGroup.Use(APIAuthMiddleware())
	{
		// Preflight handlers for the group
		apiGroup.OPTIONS("/*path", func(c *gin.Context) {
			c.Status(204)
		})

		RegisterPlanRuntimeRoutes(apiGroup, p, librarianAgent, dataAgent, coderAgent, researchCodingAgent, benchmarkAgent, chatAgent)
		RegisterUploadRoutes(apiGroup)

		apiGroup.GET("/hello", func(c *gin.Context) {
			c.String(200, "hello api group")
		})

		apiGroup.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, buildServiceHealth(c.Request.Context(), sandboxURL, optimizerURL))
		})

		apiGroup.POST("/chat", func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
			var payload ChatPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			response, err := chatAgent.Answer(c.Request.Context(), payload.Message)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"response": response,
			})
		})

		apiGroup.GET("/pdf-proxy", func(c *gin.Context) {
			pdfURL := c.Query("url")
			if pdfURL == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "url parameter is required"})
				return
			}

			validatedURL, err := validateRemotePDFURL(c.Request.Context(), pdfURL)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			log.Printf("[PDF Proxy] Fetching host: %s", validatedURL.Hostname())

			client := &http.Client{
				Transport: safeRemoteTransport(),
				Timeout:   30 * time.Second,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					if len(via) >= 5 {
						return fmt.Errorf("too many redirects")
					}
					_, err := validateRemotePDFURL(req.Context(), req.URL.String())
					return err
				},
			}

			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, validatedURL.String(), nil)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create request: %v", err)})
				return
			}

			// Add User-Agent to mimic a browser
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("[PDF Proxy] Error fetching PDF: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch PDF: %v", err)})
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("[PDF Proxy] Unexpected status code: %d", resp.StatusCode)
				c.JSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("Upstream returned status %d", resp.StatusCode)})
				return
			}

			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			if !strings.HasPrefix(contentType, "application/pdf") {
				c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "upstream response is not a PDF"})
				return
			}
			maxBytes := int64(positiveEnvInt("PDF_PROXY_MAX_MB", 32)) * 1024 * 1024
			if resp.ContentLength > maxBytes {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "PDF exceeds configured size limit"})
				return
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read upstream PDF"})
				return
			}
			if int64(len(body)) > maxBytes {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "PDF exceeds configured size limit"})
				return
			}

			c.Header("Content-Type", "application/pdf")
			c.Data(http.StatusOK, "application/pdf", body)
		})

		apiGroup.POST("/execute", func(c *gin.Context) {
			// Direct execution used to accept arbitrary client-provided inputs,
			// including workspace_path/storage_path values. Keep the old endpoint
			// as an explicit tombstone so older clients fail closed; owner-scoped
			// execution now lives at /api/plans/:id/tasks/:taskId/execute.
			c.JSON(http.StatusGone, gin.H{
				"error": "direct execution is disabled; use /api/plans/:id/tasks/:taskId/execute",
			})
			return
		})
	}
}

func buildServiceHealth(ctx context.Context, sandboxURL string, optimizerURLs ...string) gin.H {
	optimizerURL := ""
	if len(optimizerURLs) > 0 {
		optimizerURL = strings.TrimRight(strings.TrimSpace(optimizerURLs[0]), "/")
	}
	intentURL := strings.TrimRight(strings.TrimSpace(os.Getenv("INTENT_SERVICE_URL")), "/")
	gitPath, gitErr := exec.LookPath("git")
	repositoryHealth := gin.H{"ok": gitErr == nil}
	if gitErr == nil {
		repositoryHealth["git_path"] = gitPath
	} else {
		repositoryHealth["message"] = "git is unavailable; external repository workflows cannot run"
	}
	health := gin.H{
		"ok": gitErr == nil,
		"backend": gin.H{
			"ok":      true,
			"message": "backend is reachable",
		},
		"repository": repositoryHealth,
		"sandbox": gin.H{
			"ok":      false,
			"url":     sandboxURL,
			"message": "sandbox health check was not run",
		},
		"research_optimizer": gin.H{
			"ok":         optimizerURL == "",
			"configured": optimizerURL != "",
			"url":        optimizerURL,
			"message":    "research optimizer is optional and not configured",
		},
		"intent_recognition": gin.H{
			"ok":         intentURL == "",
			"configured": intentURL != "",
			"url":        intentURL,
			"message":    "intent recognition is optional and not configured",
		},
	}

	client := &http.Client{Timeout: 3 * time.Second}
	sandboxHealth := probeServiceHealth(ctx, client, sandboxURL, "/api/v1/health", true)
	health["sandbox"] = sandboxHealth
	if ok, _ := sandboxHealth["ok"].(bool); !ok {
		health["ok"] = false
	}
	if intentURL != "" {
		intentHealth := probeServiceHealth(ctx, client, intentURL, "/health", true)
		health["intent_recognition"] = intentHealth
		if ok, _ := intentHealth["ok"].(bool); !ok {
			health["ok"] = false
		}
	}
	if optimizerURL == "" {
		return health
	}
	optimizerHealth := probeServiceHealth(ctx, client, optimizerURL, "/health", true)
	health["research_optimizer"] = optimizerHealth
	if ok, _ := optimizerHealth["ok"].(bool); !ok {
		health["ok"] = false
	}
	return health
}

func probeServiceHealth(ctx context.Context, client *http.Client, baseURL, path string, configured bool) gin.H {
	result := gin.H{"ok": false, "configured": configured, "url": baseURL}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		result["message"] = err.Error()
		return result
	}
	response, err := client.Do(request)
	if err != nil {
		result["message"] = err.Error()
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result["message"] = fmt.Sprintf("health check returned HTTP %d", response.StatusCode)
		return result
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&payload); err != nil {
		result["message"] = err.Error()
		return result
	}
	for key, value := range payload {
		result[key] = value
	}
	result["configured"] = configured
	result["url"] = baseURL
	if ok, _ := result["ok"].(bool); !ok {
		result["ok"] = false
		if _, exists := result["message"]; !exists {
			result["message"] = "service reported unhealthy"
		}
	}
	return result
}

// detectIntentType 根据用户意图文本判断任务类型
func DetectIntentType(intent string) string {
	// AutoResearch 同时会出现“代码、实验、评测”等通用词，必须在论文复现和代码执行前识别。
	autoResearchKeywords := []string{
		"autoresearch", "auto research", "自主研究", "自动研究", "自动实验", "持续实验", "实验循环", "keep/reject",
	}
	autoResearchContext := []string{"实验", "试验", "指标", "评测", "仓库", "模型", "repo", "trial", "metric", "benchmark"}
	if containsAny(intent, autoResearchKeywords) ||
		(containsAny(intent, []string{"自动优化"}) && containsAny(intent, autoResearchContext)) {
		return "AutoResearch"
	}

	// 论文复现类需要优先于“对比/评估”类判断。
	// 论文复现需求常包含“与论文指标对比”等词，如果先命中“对比”会被误路由到框架评测。
	reproKeywords := []string{"复现", "reproduce", "replicate", "论文", "paper", "papers with code", "arxiv", "实现算法"}
	if containsAny(intent, reproKeywords) {
		return "Paper_Reproduction"
	}

	// 纯绘图请求优先于“对比/评估”类关键词：
	// “帮我画一个正弦函数和余弦函数的对比图”里的“对比”不是框架对比。
	if intentpkg.ContainsPlotIntent(intent) && !intentpkg.ContainsFrameworkContext(intent) {
		return "Code_Execution"
	}

	// 框架对比评测类关键词（扩展版）
	evalKeywords := []string{
		"对比", "比较", "评估", "选型", "哪个好", "哪个更", "区别", "异同",
		"rag", "langchain", "llamaindex", "llama_index", "haystack",
		"dspy", "autogen", "crewai", "langgraph", "framework",
		"框架", "测试两", "比一比", "pk", "vs",
	}
	if containsAny(intent, evalKeywords) {
		return "Framework_Evaluation"
	}

	// 代码执行类
	codeKeywords := []string{
		"计算", "代码", "运行", "执行", "画图", "分析", "写一个", "生成代码",
		"跑一下", "plot", "matplotlib", "numpy", "python",
	}
	if containsAny(intent, codeKeywords) {
		return "Code_Execution"
	}

	return "General"
}

// containsAny 检查字符串是否包含任意一个关键词（忽略大小写）
func containsAny(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, k := range keywords {
		kLower := strings.ToLower(k)
		if strings.Contains(lower, kLower) {
			return true
		}
	}
	return false
}

func buildExecuteOutputs(task *models.Task) map[string]any {
	outputs := map[string]any{}
	if task == nil {
		return outputs
	}
	if strings.TrimSpace(task.Result) != "" {
		outputs["result"] = task.Result
	}
	if strings.TrimSpace(task.Code) != "" {
		outputs["generated_code"] = task.Code
	}
	if strings.TrimSpace(task.StructuredData) != "" {
		outputs["structured_data"] = task.StructuredData
	}
	if strings.TrimSpace(task.ImageBase64) != "" {
		outputs["image_base64"] = task.ImageBase64
	}
	return outputs
}

func buildPlanClarification(intentType string, intent string) (gin.H, bool) {
	if intentType != "Paper_Reproduction" || !shouldClarifyPaperReproductionMode(intent) {
		return nil, false
	}

	fullRequested := containsAny(intent, []string{"full", "complete", "bleu", "wmt", "wmt14", "完整", "全量", "真实复现", "论文指标"})
	decision := scheduler.DecidePaperReproductionMode("auto", fullRequested, os.TempDir())
	recommendedMode := "smoke"
	if decision.FullEligible && fullRequested {
		recommendedMode = "full"
	}

	return gin.H{
		"required":         true,
		"type":             "paper_reproduction_mode",
		"recommended_mode": recommendedMode,
		"question":         buildPaperReproductionModeQuestion(decision),
		"options": []gin.H{
			{
				"id":          "smoke",
				"label":       "运行最小验证",
				"description": "快速验证论文复现链路，不下载全量数据、不训练完整模型、不计算论文 BLEU。",
			},
			{
				"id":          "full",
				"label":       "开启全量复现",
				"description": "仅在你确认本机或外部计算资源足够时开启，会进入数据下载、训练和 BLEU 评测流程。",
			},
		},
		"mode_decision":  decision,
		"resource_probe": decision.Probe,
	}, true
}

func shouldClarifyPaperReproductionMode(intent string) bool {
	lower := strings.ToLower(strings.TrimSpace(intent))
	if lower == "" {
		return false
	}
	explicitSmoke := containsAny(lower, []string{"最小实验", "最小验证", "smoke", "quick", "快速验证"})
	explicitFull := containsAny(lower, []string{
		"full reproduction", "full run", "enable full", "run wmt",
		"完整复现", "全量复现", "完整实验", "全量实验", "开启全量", "采用 full",
	})
	if explicitSmoke && !explicitFull {
		return false
	}
	return containsAny(lower, []string{
		"bleu", "wmt", "wmt14", "full", "complete",
		"完整复现", "全量复现", "完整实验", "全量实验", "真实复现", "论文指标",
	})
}

func buildPaperReproductionModeQuestion(decision scheduler.ReproductionModeDecision) string {
	probe := decision.Probe
	status := fmt.Sprintf("检测到这是论文复现场景，可能涉及全量训练/BLEU 评测。本机资源探测：CPU=%d，内存=%.1fGB，可用磁盘=%.1fGB，CUDA GPU=%d。",
		probe.CPUCount, probe.MemoryGB, probe.DiskFreeGB, probe.GPUCount)
	if decision.FullEligible {
		return status + " 当前资源满足 full reproduction 门槛。是否确认开启全量复现模式？"
	}
	return status + " 当前资源未满足默认 full reproduction 门槛，建议先运行 smoke 最小验证。你是否确认有足够的本机或外部资源并仍要开启全量复现？"
}

func collectPaperSearchFields(intentCtx models.IntentContext, rawIntent string) map[string]any {
	fields := map[string]any{}

	if rawFields, ok := intentCtx.Metadata["paper_search_fields"].(map[string]any); ok {
		for key, value := range rawFields {
			if strings.TrimSpace(fmt.Sprint(value)) != "" {
				fields[key] = value
			}
		}
	}
	for _, key := range []string{"paper_title", "paper_arxiv_id", "paper_search_query", "paper_method_name"} {
		if value, ok := intentCtx.Entities[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			fields[key] = value
		}
	}

	if _, ok := fields["paper_arxiv_id"]; !ok {
		if arxivID := routePaperArxivIDRe.FindString(rawIntent); arxivID != "" {
			fields["paper_arxiv_id"] = arxivID
		}
	}
	if _, ok := fields["paper_title"]; !ok {
		if title := extractQuotedPaperTitle(rawIntent); title != "" {
			fields["paper_title"] = title
		}
	}
	if _, ok := fields["paper_search_query"]; !ok {
		arxivID := stringFieldFromMap(fields, "paper_arxiv_id")
		title := stringFieldFromMap(fields, "paper_title")
		method := stringFieldFromMap(fields, "paper_method_name")
		switch {
		case arxivID != "":
			fields["paper_search_query"] = arxivID
		case title != "":
			fields["paper_search_query"] = title
		case method != "":
			fields["paper_search_query"] = method
		}
	}
	return fields
}

func extractQuotedPaperTitle(rawIntent string) string {
	for _, pair := range [][2]string{
		{"《", "》"},
		{"\"", "\""},
		{"'", "'"},
	} {
		start := strings.Index(rawIntent, pair[0])
		end := strings.LastIndex(rawIntent, pair[1])
		if start < 0 || end <= start {
			continue
		}
		title := strings.TrimSpace(rawIntent[start+len(pair[0]) : end])
		if title != "" && len(title) <= 240 {
			return title
		}
	}
	return ""
}

func stringFieldFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

type planUsageNode struct {
	TaskID           string              `json:"task_id"`
	Name             string              `json:"name"`
	Type             string              `json:"type"`
	Status           string              `json:"status"`
	PromptTokens     int                 `json:"prompt_tokens"`
	CompletionTokens int                 `json:"completion_tokens"`
	Calls            int                 `json:"calls"`
	CostUSD          float64             `json:"cost_usd"`
	Estimated        bool                `json:"estimated,omitempty"`
	CostSource       string              `json:"cost_source,omitempty"`
	DurationSeconds  float64             `json:"duration_seconds,omitempty"`
	SandboxSeconds   float64             `json:"sandbox_seconds,omitempty"`
	Models           []models.ModelUsage `json:"models,omitempty"`
}

type planUsageModel struct {
	Model            string  `json:"model"`
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostSource       string  `json:"cost_source,omitempty"`
}

func (r *planRuntime) planUsage(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	nodes := make([]planUsageNode, 0, len(plan.Nodes))
	modelTotals := map[string]*planUsageModel{}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		entry := planUsageNode{TaskID: node.ID, Name: node.Name, Type: node.Type, Status: string(node.Status)}
		if node.Usage != nil {
			entry.PromptTokens = node.Usage.LLMPromptTokens
			entry.CompletionTokens = node.Usage.LLMCompletionTokens
			entry.Calls = node.Usage.LLMCalls
			entry.CostUSD = node.Usage.LLMCostUSD
			entry.Estimated = node.Usage.LLMEstimated
			entry.CostSource = node.Usage.CostSource
			entry.DurationSeconds = node.Usage.DurationSeconds
			entry.SandboxSeconds = node.Usage.SandboxSeconds
			entry.Models = append([]models.ModelUsage(nil), node.Usage.ModelBreakdown...)
			for _, modelUsage := range node.Usage.ModelBreakdown {
				total := modelTotals[modelUsage.Model]
				if total == nil {
					total = &planUsageModel{Model: modelUsage.Model}
					modelTotals[modelUsage.Model] = total
				}
				total.Calls += modelUsage.Calls
				total.PromptTokens += modelUsage.PromptTokens
				total.CompletionTokens += modelUsage.CompletionTokens
				total.CostUSD += modelUsage.CostUSD
				total.Estimated = total.Estimated || modelUsage.Estimated
				total.CostSource = weakerCostSourceLabel(total.CostSource, modelUsage.CostSource)
			}
		}
		nodes = append(nodes, entry)
	}
	modelsList := make([]planUsageModel, 0, len(modelTotals))
	for _, model := range modelTotals {
		modelsList = append(modelsList, *model)
	}
	sort.Slice(modelsList, func(i, j int) bool { return modelsList[i].CostUSD > modelsList[j].CostUSD })
	c.JSON(http.StatusOK, gin.H{
		"plan_id": plan.ID,
		"usage":   plan.Usage,
		"nodes":   nodes,
		"models":  modelsList,
		"budget":  plan.Budget,
	})
}

func weakerCostSourceLabel(left, right string) string {
	rank := func(value string) int {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "profile":
			return 3
		case "env":
			return 2
		case "estimated":
			return 1
		case "unknown":
			return 0
		default:
			return -1
		}
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if rank(left) <= rank(right) {
		return left
	}
	return right
}

func (r *planRuntime) usageSummary(c *gin.Context) {
	userID := resolveUserID(c)
	period := strings.ToLower(strings.TrimSpace(c.DefaultQuery("period", "day")))
	if period != "month" {
		period = "day"
	}
	if r.budget == nil {
		c.JSON(http.StatusOK, gin.H{"summary": gin.H{"owner_id": userID, "period": period, "configured": false, "top_plans": []any{}, "top_models": []any{}}})
		return
	}
	summary := r.budget.Summary(userID, period)
	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

func (r *planRuntime) usageTimeseries(c *gin.Context) {
	userID := resolveUserID(c)
	group := strings.ToLower(strings.TrimSpace(c.DefaultQuery("group", "day")))
	if group != "day" && group != "model" && group != "plan" {
		group = "day"
	}
	from := parseTimeQuery(c.Query("from"))
	to := parseTimeQuery(c.Query("to"))
	if r.budget == nil {
		c.JSON(http.StatusOK, gin.H{"points": []any{}, "group": group})
		return
	}
	points := r.budget.Timeseries(userID, from, to, group)
	c.JSON(http.StatusOK, gin.H{"points": points, "group": group})
}

func parseTimeQuery(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

type budgetPayload struct {
	Period   string   `json:"period"`
	LimitUSD *float64 `json:"limit_usd"`
	Policy   string   `json:"policy"`
}

func (r *planRuntime) getBudget(c *gin.Context) {
	userID := resolveUserID(c)
	if r.budget == nil {
		c.JSON(http.StatusOK, gin.H{"budget": nil})
		return
	}
	budget, ok := r.budget.Budget(userID)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"budget": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"budget": budget})
}

func (r *planRuntime) putBudget(c *gin.Context) {
	userID := resolveUserID(c)
	var payload budgetPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid budget payload", "code": "invalid_budget"})
		return
	}
	if payload.LimitUSD == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit_usd is required", "code": "invalid_budget"})
		return
	}
	period := strings.ToLower(strings.TrimSpace(payload.Period))
	if period == "" {
		period = "day"
	}
	if r.budget == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "budget ledger unavailable", "code": "budget_unavailable"})
		return
	}
	budget, err := r.budget.SetBudget(userID, period, *payload.LimitUSD, payload.Policy)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_budget"})
		return
	}
	recordAuditEvent("budget_updated", userID, userID, map[string]any{"period": budget.Period, "limit_usd": budget.LimitUSD, "policy": budget.Policy})
	c.JSON(http.StatusOK, gin.H{"budget": budget})
}

func (r *planRuntime) budgetAlerts(c *gin.Context) {
	userID := resolveUserID(c)
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	if r.budget == nil {
		c.JSON(http.StatusOK, gin.H{"alerts": []any{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": r.budget.Alerts(userID, limit)})
}

package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiinternal "research-assistant-backend/internal/api"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"

	"github.com/gin-gonic/gin"
)

func TestDetectIntentType_FrameworkEvaluationExample(t *testing.T) {
	intent := "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子"
	if got := apiinternal.DetectIntentType(intent); got != "Framework_Evaluation" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "Framework_Evaluation")
	}
}

func TestDetectIntentType_DoesNotMisclassifyGenericQuestion(t *testing.T) {
	intent := "介绍一下多智能体系统的基本概念"
	if got := apiinternal.DetectIntentType(intent); got != "General" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "General")
	}
}

func TestDetectIntentType_PaperReproductionBeatsComparisonKeyword(t *testing.T) {
	intent := "请复现论文《Attention Is All You Need》，通过 Papers with Code 找到真实开源实现，运行最小实验并和论文指标对比"
	if got := apiinternal.DetectIntentType(intent); got != "Paper_Reproduction" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "Paper_Reproduction")
	}
}

func TestPlanRoute_FrameworkEvaluationExample(t *testing.T) {
	plan := createRuntimePlan(t, "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比")
	if len(plan.Nodes) < 4 {
		t.Fatalf("expected a multi-step framework plan, got %d nodes", len(plan.Nodes))
	}
	var coderCount, sandboxCount, dataCount int
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.AssignedTo {
		case "coder_agent":
			coderCount++
		case "sandbox_agent":
			sandboxCount++
		case "data_agent":
			dataCount++
		}
	}
	if coderCount == 0 || sandboxCount == 0 || dataCount == 0 {
		t.Fatalf("unexpected framework distribution: coder=%d sandbox=%d data=%d", coderCount, sandboxCount, dataCount)
	}
}

func TestPlanRoute_PaperReproductionExample(t *testing.T) {
	plan := createRuntimePlan(t, "请复现论文《Attention Is All You Need》，通过 Papers with Code 找到真实开源实现，运行最小实验并和论文指标对比")
	if len(plan.Nodes) < 8 {
		t.Fatalf("expected a full paper reproduction plan, got %d nodes", len(plan.Nodes))
	}
	var sawPaperParse, sawClaimRubric, sawRepository, sawPaperCompare, sawClaimEvidence bool
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		name := strings.ToLower(node.Name + " " + node.Type)
		sawPaperParse = sawPaperParse || strings.Contains(name, "parse") || strings.Contains(name, "解析论文")
		sawClaimRubric = sawClaimRubric || strings.Contains(name, "claim_rubric") || strings.Contains(name, "claim rubric")
		sawRepository = sawRepository || strings.Contains(name, "repo_discovery") || strings.Contains(name, "repository") || strings.Contains(name, "开源仓库")
		sawPaperCompare = sawPaperCompare || strings.Contains(name, "paper_compare") || strings.Contains(name, "compare")
		sawClaimEvidence = sawClaimEvidence || strings.Contains(name, "claim_evidence") || strings.Contains(name, "claim-to-evidence")
	}
	if !sawPaperParse || !sawClaimRubric || !sawRepository || !sawPaperCompare || !sawClaimEvidence {
		t.Fatalf("paper reproduction plan missing canonical steps: parse=%t rubric=%t repository=%t compare=%t evidence=%t", sawPaperParse, sawClaimRubric, sawRepository, sawPaperCompare, sawClaimEvidence)
	}
}

func TestPlanRoute_PaperFullReproductionClarification(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiinternal.RegisterPlanRuntimeRoutes(r.Group("/api"), planner.NewPlanner(), nil, nil, nil)

	body := []byte(`{"intent":"请真实复现论文 Attention Is All You Need，跑 WMT14 BLEU 并和论文指标对比"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Clarification struct {
			Required        bool   `json:"required"`
			Type            string `json:"type"`
			RecommendedMode string `json:"recommended_mode"`
			Question        string `json:"question"`
			ResourceProbe   struct {
				CPUCount int `json:"cpu_count"`
				GPUCount int `json:"gpu_count"`
			} `json:"resource_probe"`
		} `json:"clarification"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Clarification.Required {
		t.Fatalf("expected clarification to be required, body=%s", rec.Body.String())
	}
	if response.Clarification.Type != "paper_reproduction_mode" {
		t.Fatalf("unexpected clarification type: %q", response.Clarification.Type)
	}
	if response.Clarification.Question == "" || !strings.Contains(response.Clarification.Question, "CUDA GPU") {
		t.Fatalf("expected resource-aware question, got %q", response.Clarification.Question)
	}
	if response.Clarification.RecommendedMode == "" {
		t.Fatalf("expected recommended mode")
	}
	if response.Clarification.ResourceProbe.CPUCount <= 0 {
		t.Fatalf("expected cpu resource probe, got %+v", response.Clarification.ResourceProbe)
	}
}

func createRuntimePlan(t *testing.T, intent string) models.PlanGraph {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiinternal.RegisterPlanRuntimeRoutes(r.Group("/api"), planner.NewPlanner(), nil, nil, nil)

	body, err := json.Marshal(map[string]string{"intent": intent})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Message   string           `json:"message"`
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Message != "Plan generated successfully" {
		t.Fatalf("unexpected message: %q", response.Message)
	}
	if response.PlanGraph.ID == "" {
		t.Fatal("plan_graph.id is empty")
	}
	return response.PlanGraph
}

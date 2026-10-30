package api

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"research-assistant-backend/internal/models"
)

// reviewRiskHighTasks are node types that touch expensive or externally
// visible resources. Their presence alone is enough to require a review pass.
var reviewRiskHighTasks = map[string]bool{
	"autoresearch_run":            true,
	"autoresearch_validate":       true,
	"paper_code_execute":          true,
	"benchmark_execute":           true,
	"experiment_run":              true,
	"fix_and_rerun":               true,
	"benchmark_adapter_preflight": true,
}

// reviewRiskMediumTasks install images/packages or download data/models.
var reviewRiskMediumTasks = map[string]bool{
	"image_prepare":        true,
	"install_dependencies": true,
	"runtime_probe":        true,
	"dataset_profile":      true,
}

// reviewCostThresholdUSD reads the deployment review threshold. Zero disables
// the cost trigger so simple deployments do not gate every plan.
func reviewCostThresholdUSD() float64 {
	raw := strings.TrimSpace(os.Getenv("REVIEW_COST_THRESHOLD_USD"))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// estimatePlanCostUSD is a conservative pre-execution estimate used only for
// the review gate threshold and the UI badge. It deliberately does not write
// RunUsage: that must stay a record of measured spend.
func estimatePlanCostUSD(plan *models.PlanGraph) float64 {
	if plan == nil {
		return 0
	}
	perTask := 0.02
	if raw := strings.TrimSpace(os.Getenv("BUDGET_DEFAULT_TASK_ESTIMATE_USD")); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil && value > 0 {
			perTask = value
		}
	}
	estimate := float64(len(plan.Nodes)) * perTask
	if plan.Budget.MaxCostUSD > 0 {
		estimate += plan.Budget.MaxCostUSD * 0.5
	}
	return estimate
}

// planReviewDecision computes whether the plan must be reviewed before it may
// execute. It returns the review state that the plan should persist.
func planReviewDecision(plan *models.PlanGraph, requireReview bool) models.ReviewState {
	state := models.ReviewState{Status: models.ReviewNotRequired}
	if plan == nil {
		return state
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_REVIEW_MODE")))
	threshold := reviewCostThresholdUSD()
	estimated := estimatePlanCostUSD(plan)
	risk := planReviewRisk(plan)
	reasons := []string{}
	triggered := false
	if mode == "always" {
		triggered = true
		reasons = append(reasons, "deployment requires review for every plan")
	}
	if requireReview {
		triggered = true
		reasons = append(reasons, "user requested review before execution")
	}
	if threshold > 0 && estimated > threshold {
		triggered = true
		reasons = append(reasons, fmt.Sprintf("estimated cost %.2f USD exceeds threshold %.2f USD", estimated, threshold))
	}
	if risk == "high" {
		triggered = true
		reasons = append(reasons, "plan contains high-risk or long-running tasks")
	}
	if len(plan.MissingRequirements) > 0 {
		triggered = true
		reasons = append(reasons, "plan has missing requirements")
	}
	if strings.EqualFold(plan.Meta.Complexity, "L4") {
		triggered = true
		reasons = append(reasons, "plan complexity is L4")
	}
	if !triggered {
		if risk == "medium" && threshold == 0 {
			// Medium risk alone is not enough: review must not slow down every
			// ordinary plotting/coding plan.
			return state
		}
		return state
	}
	state.Required = true
	state.Status = models.ReviewPending
	state.Reason = strings.Join(reasons, "; ")
	state.Risk = risk
	state.EstimatedUSD = estimated
	return state
}

// planReviewRisk classifies the plan by its most risky node.
func planReviewRisk(plan *models.PlanGraph) string {
	if plan == nil {
		return "low"
	}
	risk := "low"
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if reviewRiskHighTasks[node.Type] {
			return "high"
		}
		if reviewRiskMediumTasks[node.Type] && risk == "low" {
			risk = "medium"
		}
	}
	if spec := planRuntimeSpec(plan); spec.GPURequired || (len(spec.DatasetRefs) > 0 && risk == "low") || (len(spec.ModelRefs) > 0 && risk == "low") {
		if spec.GPURequired {
			return "high"
		}
		risk = "medium"
	}
	return risk
}

// applyReviewGate mutates the plan status for a required review.
func applyReviewGate(plan *models.PlanGraph, requireReview bool) {
	if plan == nil {
		return
	}
	state := planReviewDecision(plan, requireReview)
	plan.Review = state
	if !state.Required {
		return
	}
	// A legacy high-risk approval gate takes precedence: the user must decide
	// the runtime permission first, then the review gate can be approved.
	if plan.Approval.Required && plan.Approval.Status != "approved" {
		plan.Review.Status = models.ReviewPending
		return
	}
	if state.Status == models.ReviewPending && plan.Status == models.StatusPending {
		plan.Status = models.StatusAwaitingReview
	}
}

// planRuntimeSpec decodes the reproduction runtime spec attached to a plan.
// The scheduler has its own copy; this one keeps the review gate in the API
// layer without importing scheduler internals.
func planRuntimeSpec(plan *models.PlanGraph) models.ReproductionRuntimeSpec {
	var spec models.ReproductionRuntimeSpec
	if plan == nil {
		return spec
	}
	artifact, ok := plan.Artifacts["reproduction_runtime_spec"]
	if !ok {
		return spec
	}
	_ = json.Unmarshal([]byte(artifact.EffectiveValue()), &spec)
	return spec
}

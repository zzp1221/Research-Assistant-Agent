package planner

import (
	"context"
	"fmt"
	"log"
	"os"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Planner generates DAGs based on user intent
type Planner struct {
	agent *plannerAgent
}

func NewPlanner() *Planner {
	return &Planner{
		agent: newPlannerAgent(),
	}
}

// BuildPlan creates the graph-based plan using the default complexity route.
// It is the compatibility entry point; callers that already resolved
// attachments/research context should prefer BuildPlanWithRoute.
func (p *Planner) BuildPlan(ctx context.Context, intent models.IntentContext) (*models.PlanGraph, error) {
	return p.BuildPlanWithRoute(ctx, intent, RouteDecision{})
}

// BuildPlanWithRoute creates the plan for an explicit RouteDecision.
func (p *Planner) BuildPlanWithRoute(ctx context.Context, intent models.IntentContext, route RouteDecision) (*models.PlanGraph, error) {
	startedAt := time.Now()
	plan := &models.PlanGraph{
		ID:         uuid.New().String(),
		TraceID:    "trace_" + strings.ReplaceAll(uuid.New().String(), "-", ""),
		UserIntent: intent.RawIntent,
		IntentType: intent.IntentType,
		Status:     models.StatusPending,
		Nodes:      []*models.TaskNode{},
		Edges:      []*models.TaskEdge{},
		Artifacts:  map[string]models.Artifact{},
		Budget: models.RunBudget{
			MaxTaskAttempts: 100,
			MaxDurationSec:  2100,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	router := NewComplexityRouterFromEnv()
	if route.IsZero() {
		route = router.Decide(ctx, intent, nil, nil)
	}
	plan.Meta.PlanningRoute = planningRouteFor(route)
	plan.Meta.Complexity = string(route.Complexity)
	plan.Meta.ComplexitySource = route.Source
	plan.Meta.TemplateVariant = route.Variant

	nodes, plannerSource, plannerFallbackReason := p.selectPlanNodes(ctx, intent, route)
	if len(nodes) == 0 {
		nodes = buildFallbackNodes(intent)
		plannerSource = "template"
	}
	if plannerFallbackReason != "" {
		metrics.RecordPlannerFallback(plannerFallbackCategory(plannerFallbackReason))
	}
	_ = startedAt

	normalizeArtifactContracts(nodes)
	nodes = ensureVerificationNode(nodes)
	normalizeArtifactContracts(nodes)
	edges := buildEdgesFromNodes(nodes)
	fillInitialStatuses(nodes)
	plan.Nodes = nodes
	plan.Edges = edges
	if isDatasetExperimentIntent(intent) {
		inputs := buildDatasetExperimentInputs(intent)
		if wallSeconds, ok := inputs["experiment_max_wall_seconds"].(int); ok {
			plan.Budget.MaxDurationSec = wallSeconds + 900
		}
	} else if isAutoResearchIntent(intent) {
		inputs := buildAutoResearchInputs(intent)
		if wallSeconds, ok := inputs["autoresearch_max_wall_seconds"].(int); ok {
			plan.Budget.MaxDurationSec = wallSeconds + 900
		}
	}
	plan.Meta.PlannerSource = plannerSource
	plan.Meta.PlannerFallbackReason = plannerFallbackReason
	if intent.IntentType == "General" {
		shape := "legacy"
		if generalPlanFastPathEnabled() && route.Source != "disabled" {
			shape = string(classifyGeneralPlanShape(intent))
		}
		plan.Meta.GeneralPlanShape = shape
		metrics.RecordPlanGeneralShape(shape)
		metrics.RecordPlanLLMNodes(intent.IntentType, shape, countGeneralLLMNodes(nodes))
	}
	fillGraphMeta(plan)

	// A planner-agent blueprint can pass its own contract checks yet still leave
	// required artifacts without a producer. Fall back to the deterministic
	// template rather than failing the whole request.
	if err := validatePlanGraph(plan); err != nil {
		logPlannerFallback(intent, err)
		metrics.RecordPlannerFallback("graph_validation")
		if intent.IntentType != "" {
			templateNodes := buildFallbackNodes(intent)
			if len(templateNodes) > 0 {
				nodes = templateNodes
				normalizeArtifactContracts(nodes)
				edges := buildEdgesFromNodes(nodes)
				fillInitialStatuses(nodes)
				plan.Nodes = nodes
				plan.Edges = edges
				plan.Meta.PlannerSource = "template"
				plan.Meta.PlannerFallbackReason = err.Error()
				plan.Meta.PlanningRoute = "template_fallback"
				fillGraphMeta(plan)
			}
		}
		metrics.RecordPlanRoute(string(route.Complexity), route.Source, route.Variant)
		metrics.RecordPlanCreateDuration(string(route.Complexity), route.Source, time.Since(startedAt))
		return plan, validatePlanGraph(plan)
	}
	metrics.RecordPlanRoute(string(route.Complexity), route.Source, route.Variant)
	metrics.RecordPlanCreateDuration(string(route.Complexity), route.Source, time.Since(startedAt))
	return plan, nil
}

// planningRouteFor maps a route decision to the observable planning path.
func planningRouteFor(route RouteDecision) string {
	if route.Source == "disabled" {
		return "template_fallback"
	}
	switch levelRank(route.Complexity) {
	case 0, 1:
		return "fast_path"
	case 2:
		return "standard_template"
	default:
		return "agent_augmented"
	}
}

// plannerFallbackCategory reduces a free-form error to a bounded metric label.
func plannerFallbackCategory(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "disabled"):
		return "agent_disabled"
	case strings.Contains(lower, "node limit"), strings.Contains(lower, "above the"):
		return "node_limit"
	case strings.Contains(lower, "validation"), strings.Contains(lower, "validate"):
		return "validation"
	case strings.Contains(lower, "parse"):
		return "parse_error"
	default:
		return "error"
	}
}

func maxAgentPlanNodesForRoute(route RouteDecision) int {
	switch levelRank(route.Complexity) {
	case 3:
		return envIntDefault("PLANNER_MAX_NODES_L3", 16)
	case 4:
		return envIntDefault("PLANNER_MAX_NODES_L4", 24)
	default:
		return maxAgentPlanNodes
	}
}

func envIntDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// selectPlanNodes chooses the plan builder for a route. L0-L2 use deterministic
// templates, L3/L4 use the planner agent with a deterministic skeleton, and the
// legacy path (router disabled) keeps the previous agent-first behavior.
func (p *Planner) selectPlanNodes(ctx context.Context, intent models.IntentContext, route RouteDecision) ([]*models.TaskNode, string, string) {
	if isDatasetExperimentIntent(intent) {
		return buildDatasetExperimentNodes(intent), "template", ""
	}
	if isCustomDatasetBenchmarkIntent(intent) {
		return buildCustomDatasetBenchmarkNodes(intent), "template", ""
	}
	if isAutoResearchIntent(intent) {
		return buildAutoResearchNodes(intent), "template", ""
	}
	if intent.IntentType == "General" {
		// Both rollback switches restore the legacy 3-node General shape:
		// GENERAL_PLAN_FAST_PATH targets the General fast path, while
		// COMPLEXITY_ROUTER_ENABLED=false must restore the pre-routing planner.
		if !generalPlanFastPathEnabled() || route.Source == "disabled" {
			return buildGeneralNodesLegacy(intent), "template", ""
		}
		// General shape is deterministic (direct/research/paper/literature);
		// complexity only decides whether the research shape gets an explicit
		// verification stage.
		if route.Source != "disabled" && levelRank(route.Complexity) >= 4 {
			return buildGeneralFullNodes(intent), "template", ""
		}
		return buildGeneralNodesV2(intent), "template", ""
	}
	if route.IsZero() || route.Source == "disabled" {
		return p.buildAgentPlan(ctx, intent, nil, maxAgentPlanNodes)
	}
	switch levelRank(route.Complexity) {
	case 0, 1, 2:
		nodes, err := buildRegisteredNodesWithVariant(ctx, intent, route.Variant)
		if err != nil || len(nodes) == 0 {
			return nil, "template", errText(err)
		}
		return nodes, "template", ""
	default:
		skeleton, err := buildRegisteredNodesWithVariant(ctx, intent, "standard")
		if err != nil {
			skeleton = nil
		}
		return p.buildAgentPlan(ctx, intent, skeleton, maxAgentPlanNodesForRoute(route))
	}
}

// buildAgentPlan runs the planner agent with one bounded repair round. When a
// skeleton is supplied the agent output is merged onto it.
func (p *Planner) buildAgentPlan(ctx context.Context, intent models.IntentContext, skeleton []*models.TaskNode, limit int) ([]*models.TaskNode, string, string) {
	if p.agent == nil {
		return nil, "template", "planner agent is nil"
	}
	nodes, session, err := p.agent.BuildNodesWithSession(ctx, intent, skeleton)
	if err == nil {
		nodes = postProcessPlannerNodes(nodes)
		if len(nodes) > limit {
			err = fmt.Errorf("planner produced %d nodes, above the %d node limit", len(nodes), limit)
		} else if validationErr := validateAgentPlannedNodes(intent, nodes); validationErr != nil {
			// One bounded self-repair round with the validation error JSON
			// before falling back to the deterministic template. The repair
			// continues the same conversation so the previous nodes stay as an
			// assistant message.
			if repaired, repairErr := p.agent.RepairNodesWithSession(ctx, intent, nodes, skeleton, validationErr, session); repairErr == nil {
				repaired = postProcessPlannerNodes(repaired)
				if len(repaired) <= limit && validateAgentPlannedNodes(intent, repaired) == nil {
					log.Printf("[PlannerAgent] self-repair succeeded after: %v", validationErr)
					nodes = repaired
					err = nil
				} else {
					err = validationErr
				}
			} else {
				err = validationErr
			}
		}
	}
	if err != nil {
		logPlannerFallback(intent, err)
		return nil, "template", err.Error()
	}
	return nodes, "agent", ""
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// buildFallbackNodes selects the deterministic template through the
// PlanBuilder registry. The switch dispatch is intentionally removed so new
// intent branches cannot silently copy template wiring.
func buildFallbackNodes(intent models.IntentContext) []*models.TaskNode {
	nodes, err := buildRegisteredNodes(context.Background(), intent)
	if err == nil && len(nodes) > 0 {
		return nodes
	}
	return buildGeneralNodesV2(intent)
}

func newNode(name, taskType, agent string, deps, requiredArtifacts, outputArtifacts []string, parallelizable bool, context string) *models.TaskNode {
	if deps == nil {
		deps = []string{}
	}
	if requiredArtifacts == nil {
		requiredArtifacts = []string{}
	}
	if outputArtifacts == nil {
		outputArtifacts = []string{}
	}

	now := time.Now()
	displayName := bilingualTaskName(name)
	node := &models.TaskNode{
		ID:                uuid.New().String(),
		Name:              displayName,
		Type:              taskType,
		Description:       prompts.TaskDescription(displayName, context),
		AssignedTo:        agent,
		Status:            models.StatusPending,
		Dependencies:      deps,
		RequiredArtifacts: requiredArtifacts,
		OutputArtifacts:   outputArtifacts,
		Parallelizable:    parallelizable,
		Priority:          0,
		RetryLimit:        1,
		RunCount:          0,
		TimeoutSeconds:    defaultTaskTimeoutSeconds(taskType),
		Contract: models.TaskContract{
			Version:         models.TaskContractVersion,
			InputArtifacts:  append([]string(nil), requiredArtifacts...),
			OutputArtifacts: append([]string(nil), outputArtifacts...),
			InputSchema:     "task.input/v1",
			OutputSchema:    "task.output/v1",
			AllowedTools:    allowedToolsForAgent(agent),
		},
		Inputs:    map[string]any{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Fail fast when a template asks for an unregistered task type, a dead type,
	// or an agent that is not allowed for that type. A fake node must never reach
	// the scheduler.
	if err := validateTaskNode(node); err != nil {
		// Keep constructing the node so the caller can fall back cleanly; the
		// plan-level validator will reject it instead of crashing the process.
		node.Error = err.Error()
		log.Printf("planner built invalid task node %q: %v", displayName, err)
	}
	return node
}

// defaultTaskTimeoutSeconds gives heavy execution tasks a realistic budget.
// Dependency installs and full experiment runs legitimately exceed ten minutes
// on slow mirrors; the value is overridable per deployment.
func defaultTaskTimeoutSeconds(taskType string) int {
	envInt := func(key string, fallback int) int {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			return fallback
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return fallback
		}
		return value
	}
	switch taskType {
	case "install_dependencies":
		return envInt("TASK_TIMEOUT_INSTALL_SECONDS", 1800)
	case "paper_code_execute", "benchmark_execute", "autoresearch_run", "experiment_run", "benchmark_adapter_preflight":
		return envInt("TASK_TIMEOUT_EXECUTE_SECONDS", 3600)
	case "render_plot", "result_visualization":
		return envInt("TASK_TIMEOUT_PLOT_SECONDS", 900)
	default:
		return envInt("TASK_TIMEOUT_DEFAULT_SECONDS", 600)
	}
}

func allowedToolsForAgent(agent string) []string {
	// A custom AgentProfile owns its allowlist; the planner must not invent a
	// second one. The executor intersects this nil with the profile allowlist.
	if strings.HasPrefix(strings.TrimSpace(agent), "profile:") {
		return nil
	}
	switch agent {
	case "coder_agent":
		return []string{"repository.read", "workspace.write", "code.generate"}
	case "sandbox_agent":
		return []string{"sandbox.create", "sandbox.command", "sandbox.python", "sandbox.delete"}
	case "librarian_agent":
		return []string{"paper.search", "paper.read", "repository.discover"}
	case "data_agent":
		return []string{"artifact.read", "metrics.analyze", "report.write"}
	case "research_coding_agent":
		return []string{"repository.read", "repository.patch_scoped", "dataset.profile", "workspace.write_scoped", "sandbox.command", "metrics.validate", "research.trial_ledger"}
	case "benchmark_agent":
		return []string{"dataset.audit", "dataset.split_scoped", "dataset.leakage_check", "metrics.contract", "reward.contract", "metrics.recompute_hidden"}
	default:
		return []string{"conversation.respond"}
	}
}

func logPlannerFallback(intent models.IntentContext, err error) {
	log.Printf("[PlannerAgent] fallback to template planner intent_type=%s raw_intent=%q err=%v", intent.IntentType, intent.RawIntent, err)
}

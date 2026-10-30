package api

import (
	"fmt"
	"sort"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

// validationError is one actionable problem found while validating an edited
// plan revision.
type validationError struct {
	NodeID      string `json:"node_id,omitempty"`
	Field       string `json:"field,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

// planValidationResult is the structured response of the validate endpoint.
type planValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []validationError `json:"errors"`
}

func newValidationError(nodeID, field, message, remediation string) validationError {
	return validationError{NodeID: nodeID, Field: field, Message: message, Remediation: remediation}
}

// validatePlanGraph performs the first-phase constraint checks for an edited
// plan: DAG integrity, task registry membership, dependency/artifact
// satisfaction and bounded budget values.
func validatePlanGraph(plan *models.PlanGraph) planValidationResult {
	result := planValidationResult{Valid: true, Errors: []validationError{}}
	if plan == nil {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "", "plan graph is required", "提供完整 PlanGraph"))
		return result
	}
	if len(plan.Nodes) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "nodes", "plan has no nodes", "至少保留一个任务节点"))
		return result
	}
	byID := map[string]*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node == nil {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError("", "nodes", "plan contains a nil node", "删除空节点"))
			continue
		}
		if strings.TrimSpace(node.ID) == "" {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError("", "id", "node id is required", "为节点分配唯一 id"))
			continue
		}
		if _, exists := byID[node.ID]; exists {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "id", "duplicate node id", "使用唯一 id"))
			continue
		}
		byID[node.ID] = node
		if err := planner.ValidatePlanTaskNode(node); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "type", err.Error(), "使用 task registry 中的类型与允许的 assigned_to"))
		}
		if node.TimeoutSeconds < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "timeout_seconds", "timeout_seconds must be non-negative", "设为 0（默认）或正数"))
		}
		if node.RetryLimit < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "retry_limit", "retry_limit must be non-negative", "设为 0 或正数"))
		}
		if node.RetryLimit > 10 {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "retry_limit", "retry_limit exceeds the system maximum of 10", "降低重试次数"))
		}
		for _, dependency := range node.Dependencies {
			if strings.TrimSpace(dependency) == "" {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "dependencies", "dependency id must not be empty", "删除空依赖"))
				continue
			}
			if _, ok := byID[dependency]; !ok {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "dependencies", "dependency not found: "+dependency, "移除悬挂依赖或补回节点"))
			}
			if dependency == node.ID {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "dependencies", "node depends on itself", "删除自依赖"))
			}
		}
	}
	// Edges must reference live nodes; dangling edges are rejected.
	for _, edge := range plan.Edges {
		if edge == nil {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError("", "edges", "plan contains a nil edge", "删除空边"))
			continue
		}
		if _, ok := byID[edge.From]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError("", "edges", "edge from unknown node: "+edge.From, "修正边的起点"))
		}
		if _, ok := byID[edge.To]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError("", "edges", "edge to unknown node: "+edge.To, "修正边的终点"))
		}
	}
	// Cycle detection over node dependencies (the source of truth for the
	// scheduler). Node order does not matter.
	if cycle := detectDependencyCycle(plan.Nodes); len(cycle) > 0 {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "dependencies", "dependency cycle detected: "+strings.Join(cycle, " -> "), "移除循环依赖"))
	}
	// Required artifacts must be produced by a dependency (or already exist as
	// a plan artifact from an upstream run).
	producers := map[string]string{}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		for _, artifact := range node.OutputArtifacts {
			if strings.TrimSpace(artifact) != "" {
				producers[artifact] = node.ID
			}
		}
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		for _, required := range node.RequiredArtifacts {
			if strings.TrimSpace(required) == "" {
				continue
			}
			producer, ok := producers[required]
			if !ok {
				if _, exists := plan.Artifacts[required]; exists {
					continue
				}
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "required_artifacts", "required artifact has no producer: "+required, "由上游节点输出该 artifact 或移除依赖"))
				continue
			}
			if producer == node.ID {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "required_artifacts", "node requires its own output artifact: "+required, "删除自引用 artifact"))
				continue
			}
			if !dependsOnTransitively(byID, node.ID, producer) {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "dependencies", "artifact producer is not upstream: "+producer, "把生产者加入依赖链"))
			}
		}
	}
	if err := validatePlanBudgetValues(plan); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "budget", err.Error(), "使用非负且在系统上限内的预算"))
	}
	if plan.Budget.MaxCostUSD > 10000 {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "budget.max_cost_usd", "max_cost_usd exceeds the system maximum of 10000", "降低预算"))
	}
	if plan.Budget.MaxTokens > 100000000 {
		result.Valid = false
		result.Errors = append(result.Errors, newValidationError("", "budget.max_tokens", "max_tokens exceeds the system maximum", "降低预算"))
	}
	return result
}

func validatePlanBudgetValues(plan *models.PlanGraph) error {
	if plan == nil {
		return nil
	}
	if plan.Budget.MaxCostUSD < 0 || plan.Budget.MaxTokens < 0 || plan.Budget.MaxSandboxSeconds < 0 || plan.Budget.MaxDownloadBytes < 0 || plan.Budget.MaxDurationSec < 0 || plan.Budget.MaxTaskAttempts < 0 {
		return fmt.Errorf("budget values must be non-negative")
	}
	if plan.Budget.Policy != "" && plan.Budget.Policy != "soft" && plan.Budget.Policy != "hard" {
		return fmt.Errorf("budget policy must be soft or hard")
	}
	return nil
}

func detectDependencyCycle(nodes []*models.TaskNode) []string {
	state := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(id string) bool
	index := map[string]*models.TaskNode{}
	for _, node := range nodes {
		if node != nil {
			index[node.ID] = node
		}
	}
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			// Found a back edge; capture the current stack from id.
			start := 0
			for i, item := range stack {
				if item == id {
					start = i
					break
				}
			}
			cycle = append(append([]string(nil), stack[start:]...), id)
			return true
		case 2:
			return false
		}
		state[id] = 1
		stack = append(stack, id)
		node := index[id]
		if node != nil {
			for _, dependency := range node.Dependencies {
				if _, ok := index[dependency]; !ok {
					continue
				}
				if visit(dependency) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return false
	}
	ids := make([]string, 0, len(index))
	for id := range index {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if state[id] == 0 && visit(id) {
			return cycle
		}
	}
	return nil
}

func dependsOnTransitively(byID map[string]*models.TaskNode, nodeID, targetID string) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(current string) bool {
		if current == targetID {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true
		node := byID[current]
		if node == nil {
			return false
		}
		for _, dependency := range node.Dependencies {
			if walk(dependency) {
				return true
			}
		}
		return false
	}
	return walk(nodeID)
}

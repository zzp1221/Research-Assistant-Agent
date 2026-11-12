package scheduler

import (
	"research-assistant-backend/internal/models"
)

func getNodeByID(plan *models.PlanGraph, taskID string) *models.TaskNode {
	for _, node := range plan.Nodes {
		if node != nil && node.ID == taskID {
			return node
		}
	}
	return nil
}

// getOutgoingNodes returns every node connected to taskID by a control, data or
// optional edge. It is the graph-level view used for rewinds and rerun impact
// analysis: an optional consumer must be invalidated when its producer is
// rerun, even though a hard failure is never propagated through that relation.
func getOutgoingNodes(plan *models.PlanGraph, taskID string) []*models.TaskNode {
	nodes := []*models.TaskNode{}
	seen := map[string]bool{}
	for _, edge := range plan.Edges {
		if edge == nil || edge.From != taskID {
			continue
		}
		switch edge.Type {
		case "control", "data", "optional":
		default:
			continue
		}
		if seen[edge.To] {
			continue
		}
		seen[edge.To] = true
		if target := getNodeByID(plan, edge.To); target != nil {
			nodes = append(nodes, target)
		}
	}
	return nodes
}

// hardDependencyIDs is the single definition of a hard precondition: the node's
// declared Dependencies plus the producers of its required artifacts (data
// edges). Readiness, initial status and failure propagation all use it, so the
// scheduler cannot promote a node one place and block it in another.
func hardDependencyIDs(plan *models.PlanGraph, task *models.TaskNode) []string {
	if plan == nil || task == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(task.Dependencies)+2)
	appendID := func(id string) {
		if id == "" || id == task.ID || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, depID := range task.Dependencies {
		appendID(depID)
	}
	for _, edge := range plan.Edges {
		if edge == nil || edge.To != task.ID {
			continue
		}
		if edge.Type == "control" || edge.Type == "data" {
			appendID(edge.From)
		}
	}
	return out
}

// hardDependents returns the nodes that must not run when taskID stops
// unsuccessfully: every node listing it as a hard dependency.
func hardDependents(plan *models.PlanGraph, taskID string) []*models.TaskNode {
	out := []*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node == nil || node.ID == taskID {
			continue
		}
		for _, depID := range hardDependencyIDs(plan, node) {
			if depID == taskID {
				out = append(out, node)
				break
			}
		}
	}
	return out
}

// isTerminalStatus reports whether a task status ends this run.
func isTerminalStatus(status models.TaskStatus) bool {
	switch status {
	case models.StatusCompleted, models.StatusSkipped, models.StatusFailed,
		models.StatusBlocked, models.StatusCanceled:
		return true
	default:
		return false
	}
}

func isWaitingStatus(status models.TaskStatus) bool {
	switch status {
	case models.StatusAwaitingApproval, models.StatusAwaitingInput:
		return true
	default:
		return false
	}
}

// hardDependenciesSatisfied reports whether every hard dependency of a node has
// completed successfully (or was deliberately skipped).
func hardDependenciesSatisfied(plan *models.PlanGraph, task *models.TaskNode) bool {
	for _, depID := range hardDependencyIDs(plan, task) {
		dep := getNodeByID(plan, depID)
		// A deliberately skipped node satisfies the dependency: its dependents
		// continue while the skipped node itself never executes. A missing node
		// never satisfies anything.
		if dep == nil || (dep.Status != models.StatusCompleted && dep.Status != models.StatusSkipped) {
			return false
		}
	}
	return true
}

// optionalDependenciesSatisfied reports whether every optional dependency has
// ended. failed/blocked/canceled are terminal for an optional producer, but an
// awaiting_* task is not: the consumer must not step over a pending approval.
func optionalDependenciesSatisfied(plan *models.PlanGraph, task *models.TaskNode) bool {
	for _, depID := range task.OptionalDependencies {
		dep := getNodeByID(plan, depID)
		if dep == nil || !isTerminalStatus(dep.Status) {
			return false
		}
	}
	return true
}

func artifactsSatisfied(plan *models.PlanGraph, task *models.TaskNode) bool {
	for _, key := range task.RequiredArtifacts {
		if _, ok := plan.Artifacts[key]; !ok {
			return false
		}
	}
	return true
}

// optionalProducerKeys lists the artifact keys produced by optional
// dependencies. Those artifacts may exist for diagnostics but are never
// treated as trusted inputs, so they cannot satisfy a RequiredArtifacts check.
func optionalProducerKeys(plan *models.PlanGraph, task *models.TaskNode) map[string]bool {
	keys := map[string]bool{}
	for _, depID := range task.OptionalDependencies {
		dep := getNodeByID(plan, depID)
		if dep == nil {
			continue
		}
		for _, key := range dep.OutputArtifacts {
			keys[key] = true
		}
	}
	return keys
}

// requiredArtifactsSatisfied is artifactsSatisfied with the optional-producer
// rule applied: an artifact whose only producer is an optional dependency is a
// diagnostic, not a trusted input.
func requiredArtifactsSatisfied(plan *models.PlanGraph, task *models.TaskNode) bool {
	untrusted := optionalProducerKeys(plan, task)
	for _, key := range task.RequiredArtifacts {
		if untrusted[key] {
			return false
		}
		if _, ok := plan.Artifacts[key]; !ok {
			return false
		}
	}
	return true
}

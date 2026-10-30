package planner

import (
	"fmt"
	"research-assistant-backend/internal/models"
	"sort"
	"strings"

	"github.com/google/uuid"
)

func buildEdgesFromNodes(nodes []*models.TaskNode) []*models.TaskEdge {
	edges := []*models.TaskEdge{}
	for _, node := range nodes {
		for _, dep := range node.Dependencies {
			edges = append(edges, &models.TaskEdge{
				ID:   uuid.New().String(),
				From: dep,
				To:   node.ID,
				Type: "control",
			})
		}
	}

	producers := map[string]string{}
	for _, node := range nodes {
		for _, key := range node.OutputArtifacts {
			producers[key] = node.ID
		}
	}

	for _, node := range nodes {
		for _, key := range node.RequiredArtifacts {
			if producerID, ok := producers[key]; ok && producerID != node.ID {
				edges = append(edges, &models.TaskEdge{
					ID:   uuid.New().String(),
					From: producerID,
					To:   node.ID,
					Type: "data",
				})
			}
		}
	}

	return edges
}

func fillInitialStatuses(nodes []*models.TaskNode) {
	for _, node := range nodes {
		if len(node.Dependencies) == 0 && len(node.RequiredArtifacts) == 0 {
			node.Status = models.StatusReady
		} else {
			node.Status = models.StatusPending
		}
	}
}

func fillGraphMeta(plan *models.PlanGraph) {
	meta := models.GraphMeta{
		TotalNodes:            len(plan.Nodes),
		PlannerSource:         plan.Meta.PlannerSource,
		PlannerFallbackReason: plan.Meta.PlannerFallbackReason,
		PlanningRoute:         plan.Meta.PlanningRoute,
		Complexity:            plan.Meta.Complexity,
		ComplexitySource:      plan.Meta.ComplexitySource,
		TemplateVariant:       plan.Meta.TemplateVariant,
		GeneralPlanShape:      plan.Meta.GeneralPlanShape,
	}

	for _, node := range plan.Nodes {
		switch node.Status {
		case models.StatusCompleted:
			meta.CompletedNodes++
		case models.StatusFailed:
			meta.FailedNodes++
		case models.StatusBlocked:
			meta.BlockedNodes++
		case models.StatusInProgress:
			meta.InProgressNodes++
		case models.StatusReady:
			meta.ReadyNodes++
		}
	}

	plan.Meta = meta
}

// normalizeArtifactContracts applies the unified plot/execution artifact
// contract after all builders (template or agent) produced their nodes:
//   - a single execute_code node also publishes execution_image so a downstream
//     render_plot can reuse the figure instead of regenerating it;
//   - render_plot / result_visualization consume prepared_runtime and
//     execution_image when (and only when) the plan actually has a producer.
//
// Adding these only when a producer exists keeps legacy plans valid while
// eliminating the "render_plot creates a brand new sandbox" failure mode.
func normalizeArtifactContracts(nodes []*models.TaskNode) {
	if len(nodes) == 0 {
		return
	}
	executeCodeCount := 0
	var singleExecuteCode *models.TaskNode
	for _, node := range nodes {
		if node != nil && node.Type == "execute_code" {
			executeCodeCount++
			singleExecuteCode = node
		}
	}
	if executeCodeCount == 1 && singleExecuteCode != nil {
		singleExecuteCode.OutputArtifacts = ensureArtifacts(singleExecuteCode.OutputArtifacts, "execution_image")
	}

	producers := map[string]string{}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, key := range node.OutputArtifacts {
			producers[key] = node.ID
		}
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		switch node.Type {
		case "render_plot", "result_visualization":
			if producer, ok := producers["prepared_runtime"]; ok && producer != node.ID {
				node.RequiredArtifacts = ensureArtifacts(node.RequiredArtifacts, "prepared_runtime")
			} else if runtimeKey := findNamespacedRuntimeArtifact(producers, node.ID); runtimeKey != "" {
				// Framework plans publish per-branch runtimes such as
				// framework_a_prepared_runtime; reuse one instead of building an
				// ephemeral sandbox.
				node.RequiredArtifacts = ensureArtifacts(node.RequiredArtifacts, runtimeKey)
			}
			if producer, ok := producers["execution_image"]; ok && producer != node.ID {
				node.RequiredArtifacts = ensureArtifacts(node.RequiredArtifacts, "execution_image")
			}
		}
	}
}

// findNamespacedRuntimeArtifact returns a branch-namespaced prepared runtime
// (for example framework_a_prepared_runtime) when the plan has one.
func findNamespacedRuntimeArtifact(producers map[string]string, selfID string) string {
	keys := make([]string, 0, len(producers))
	for key, producer := range producers {
		if producer == selfID {
			continue
		}
		if strings.HasSuffix(key, "_prepared_runtime") || strings.HasSuffix(key, "_runtime_session") {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return keys[0]
}

func validatePlanGraph(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan graph is nil")
	}
	if err := validateTaskRegistry(plan.Nodes); err != nil {
		return err
	}

	nodeMap := make(map[string]*models.TaskNode, len(plan.Nodes))
	inDegree := make(map[string]int, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if node == nil {
			return fmt.Errorf("plan graph contains nil node")
		}
		if _, exists := nodeMap[node.ID]; exists {
			return fmt.Errorf("duplicate node id: %s", node.ID)
		}
		nodeMap[node.ID] = node
		inDegree[node.ID] = 0
	}

	artifactProducers := map[string]string{}
	for _, node := range plan.Nodes {
		for _, key := range node.OutputArtifacts {
			if existing, exists := artifactProducers[key]; exists && existing != node.ID {
				return fmt.Errorf("artifact %q has multiple producers: %s and %s", key, existing, node.ID)
			}
			artifactProducers[key] = node.ID
		}
	}

	adj := map[string][]string{}
	for _, edge := range plan.Edges {
		if edge == nil {
			return fmt.Errorf("plan graph contains nil edge")
		}
		if _, ok := nodeMap[edge.From]; !ok {
			return fmt.Errorf("edge source not found: %s", edge.From)
		}
		if _, ok := nodeMap[edge.To]; !ok {
			return fmt.Errorf("edge target not found: %s", edge.To)
		}
		if edge.Type == "control" {
			inDegree[edge.To]++
			adj[edge.From] = append(adj[edge.From], edge.To)
		}
	}

	for _, node := range plan.Nodes {
		for _, dep := range node.Dependencies {
			if _, ok := nodeMap[dep]; !ok {
				return fmt.Errorf("dependency not found for node %s: %s", node.ID, dep)
			}
		}
		for _, key := range node.RequiredArtifacts {
			producer, ok := artifactProducers[key]
			if !ok {
				return fmt.Errorf("required artifact has no producer for node %s: %s", node.ID, key)
			}
			if producer == node.ID {
				return fmt.Errorf("node %s (%s) requires its own output %q", node.ID, node.Type, key)
			}
		}
	}

	queue := make([]string, 0, len(inDegree))
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++

		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited != len(plan.Nodes) {
		return fmt.Errorf("cycle detected in control edges")
	}

	return nil
}

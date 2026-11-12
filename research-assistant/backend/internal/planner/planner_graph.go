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
	appendEdge := func(from, to, edgeType string) {
		if from == "" || to == "" || from == to {
			return
		}
		for _, existing := range edges {
			if existing != nil && existing.From == from && existing.To == to && existing.Type == edgeType {
				return
			}
		}
		edges = append(edges, &models.TaskEdge{
			ID:   uuid.New().String(),
			From: from,
			To:   to,
			Type: edgeType,
		})
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, dep := range node.Dependencies {
			appendEdge(dep, node.ID, "control")
		}
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
		for _, key := range node.RequiredArtifacts {
			if producerID, ok := producers[key]; ok && producerID != node.ID {
				appendEdge(producerID, node.ID, "data")
			}
		}
		// Optional dependencies keep an explicit relation type so cycle
		// detection, graph display and rerun analysis can see them, while the
		// scheduler never propagates a hard failure through them.
		for _, dep := range node.OptionalDependencies {
			appendEdge(dep, node.ID, "optional")
		}
	}

	return edges
}

// hardPredecessors returns the same hard precondition set the scheduler uses:
// declared dependencies plus producers of required artifacts.
func hardPredecessors(nodes []*models.TaskNode, node *models.TaskNode) []string {
	if node == nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	appendID := func(id string) {
		if id == "" || id == node.ID || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, dep := range node.Dependencies {
		appendID(dep)
	}
	producers := map[string]string{}
	for _, candidate := range nodes {
		if candidate == nil {
			continue
		}
		for _, key := range candidate.OutputArtifacts {
			producers[key] = candidate.ID
		}
	}
	for _, key := range node.RequiredArtifacts {
		if producerID, ok := producers[key]; ok {
			appendID(producerID)
		}
	}
	return out
}

// fillInitialStatuses applies the same readiness predicate the scheduler uses,
// so the planner cannot mark a node ready that the runtime would refuse.
func fillInitialStatuses(nodes []*models.TaskNode) {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if len(hardPredecessors(nodes, node)) == 0 && len(node.RequiredArtifacts) == 0 && len(node.OptionalDependencies) == 0 {
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

	// Hard and optional relations are always validated together: a pair cannot be
	// both, and a cycle may not hide across the two kinds.
	if err := validateDependencyRelations(plan); err != nil {
		return err
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
		switch edge.Type {
		case "control", "data", "optional":
			inDegree[edge.To]++
			adj[edge.From] = append(adj[edge.From], edge.To)
		default:
			return fmt.Errorf("unknown edge type %q", edge.Type)
		}
	}

	for _, node := range plan.Nodes {
		for _, dep := range node.Dependencies {
			if _, ok := nodeMap[dep]; !ok {
				return fmt.Errorf("dependency not found for node %s: %s", node.ID, dep)
			}
		}
		for _, dep := range node.OptionalDependencies {
			if _, ok := nodeMap[dep]; !ok {
				return fmt.Errorf("optional dependency not found for node %s: %s", node.ID, dep)
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
			// A "fault tolerant" plan whose core consumer needs the optional
			// producer's artifact has no fault tolerance at all.
			if optionalProducer := nodeMap[producer]; optionalProducer != nil && optionalProducer.IsOptional() {
				return fmt.Errorf("node %s requires %q produced by optional task %s; the producer must be required or the consumer needs an explicit missing branch", node.ID, key, producer)
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
		if cycle := dependencyCycle(plan); len(cycle) > 0 {
			return fmt.Errorf("cycle detected in dependency relations: %s", strings.Join(cycle, " -> "))
		}
		return fmt.Errorf("cycle detected in dependency relations")
	}

	return validateAcceptanceContract(plan)
}

// validateDependencyRelations rejects contracts that mix the two relation kinds.
func validateDependencyRelations(plan *models.PlanGraph) error {
	known := map[string]*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node != nil {
			known[node.ID] = node
		}
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		hard := map[string]bool{}
		for _, dep := range node.Dependencies {
			hard[dep] = true
		}
		seen := map[string]bool{}
		for _, dep := range node.OptionalDependencies {
			if hard[dep] {
				return fmt.Errorf("node %s declares %s as both a hard and an optional dependency", node.ID, dep)
			}
			if seen[dep] {
				return fmt.Errorf("node %s declares duplicate optional dependency %s", node.ID, dep)
			}
			if dep == node.ID {
				return fmt.Errorf("node %s has an optional dependency on itself", node.ID)
			}
			seen[dep] = true
		}
		for _, edge := range plan.Edges {
			if edge == nil || edge.To != node.ID {
				continue
			}
			if edge.Type == "optional" && hard[edge.From] {
				return fmt.Errorf("edge %s->%s is optional but the node declares it as a hard dependency", edge.From, edge.To)
			}
			if (edge.Type == "control" || edge.Type == "data") && seen[edge.From] {
				return fmt.Errorf("edge %s->%s is hard but the node declares it as an optional dependency", edge.From, edge.To)
			}
		}
	}
	return nil
}

// dependencyCycle returns one cycle over all dependency relations, if any.
func dependencyCycle(plan *models.PlanGraph) []string {
	if plan == nil {
		return nil
	}
	state := map[string]int{}
	stack := []string{}
	var cycle []string
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
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
		if node := findNode(plan, id); node != nil {
			for _, dep := range append(append([]string(nil), node.Dependencies...), node.OptionalDependencies...) {
				if findNode(plan, dep) == nil {
					continue
				}
				if visit(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return false
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if state[node.ID] == 0 && visit(node.ID) {
			return cycle
		}
	}
	return nil
}

func findNode(plan *models.PlanGraph, id string) *models.TaskNode {
	if plan == nil {
		return nil
	}
	for _, node := range plan.Nodes {
		if node != nil && node.ID == id {
			return node
		}
	}
	return nil
}

// validateAcceptanceContract enforces the pre-execution acceptance rules. Old
// plans without a contract stay valid and strictly required.
func validateAcceptanceContract(plan *models.PlanGraph) error {
	if plan == nil || plan.Acceptance == nil || plan.Acceptance.IsZero() {
		return nil
	}
	if len(plan.Acceptance.Goals) == 0 {
		return fmt.Errorf("acceptance contract must declare at least one core goal")
	}
	seen := map[string]bool{}
	for _, goal := range plan.Acceptance.Goals {
		if strings.TrimSpace(goal.ID) == "" {
			return fmt.Errorf("acceptance goal id is required")
		}
		if seen[goal.ID] {
			return fmt.Errorf("duplicate acceptance goal id: %s", goal.ID)
		}
		seen[goal.ID] = true
		if strings.TrimSpace(goal.Statement) == "" {
			return fmt.Errorf("acceptance goal %s has no statement", goal.ID)
		}
		if len(goal.EvidenceRefs) == 0 && goal.MinVerifiedClaims <= 0 {
			return fmt.Errorf("acceptance goal %s has no verifiable evidence", goal.ID)
		}
	}
	// Every task must be either core or explicitly optional, and a plan whose
	// work is entirely optional could claim success without doing anything.
	hasCore := false
	for _, node := range plan.Nodes {
		if node != nil && !node.IsOptional() {
			hasCore = true
			break
		}
	}
	if !hasCore {
		return fmt.Errorf("plan declares an acceptance contract but every task is optional")
	}
	for _, id := range plan.Acceptance.RequiredTaskIDs {
		node := findNode(plan, id)
		if node == nil {
			return fmt.Errorf("acceptance requires unknown task %s", id)
		}
		if node.IsOptional() {
			return fmt.Errorf("acceptance requires task %s which is marked optional", id)
		}
	}
	return nil
}

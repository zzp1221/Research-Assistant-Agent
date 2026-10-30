package api

import (
	"sort"
	"strings"

	"research-assistant-backend/internal/models"
)

// diffEntry is one field-level change between two plan revisions.
type diffEntry struct {
	Kind   string `json:"kind"`
	NodeID string `json:"node_id,omitempty"`
	Field  string `json:"field,omitempty"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// planDiff is the response of GET /plans/:id/diff.
type planDiff struct {
	From int         `json:"from"`
	To   int         `json:"to"`
	Diff []diffEntry `json:"diff"`
}

// diffPlanGraphs compares two graphs and returns node, edge and budget changes.
func diffPlanGraphs(fromGraph, toGraph *models.PlanGraph) planDiff {
	result := planDiff{Diff: []diffEntry{}}
	if fromGraph == nil || toGraph == nil {
		return result
	}
	fromNodes := map[string]*models.TaskNode{}
	toNodes := map[string]*models.TaskNode{}
	for _, node := range fromGraph.Nodes {
		if node != nil {
			fromNodes[node.ID] = node
		}
	}
	for _, node := range toGraph.Nodes {
		if node != nil {
			toNodes[node.ID] = node
		}
	}
	ids := make([]string, 0, len(fromNodes)+len(toNodes))
	seen := map[string]bool{}
	for id := range fromNodes {
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range toNodes {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		before, hadBefore := fromNodes[id]
		after, hadAfter := toNodes[id]
		switch {
		case !hadBefore && hadAfter:
			result.Diff = append(result.Diff, diffEntry{Kind: "node_added", NodeID: id, After: after.Name})
		case hadBefore && !hadAfter:
			result.Diff = append(result.Diff, diffEntry{Kind: "node_removed", NodeID: id, Before: before.Name})
		default:
			result.Diff = append(result.Diff, diffNodeFields(id, before, after)...)
		}
	}
	// Edges are compared by from->to->type identity; ids may be regenerated.
	fromEdges := map[string]*models.TaskEdge{}
	toEdges := map[string]*models.TaskEdge{}
	edgeKey := func(edge *models.TaskEdge) string {
		return edge.From + "->" + edge.To + ":" + edge.Type
	}
	for _, edge := range fromGraph.Edges {
		if edge != nil {
			fromEdges[edgeKey(edge)] = edge
		}
	}
	for _, edge := range toGraph.Edges {
		if edge != nil {
			toEdges[edgeKey(edge)] = edge
		}
	}
	edgeKeys := make([]string, 0, len(fromEdges)+len(toEdges))
	seenEdge := map[string]bool{}
	for key := range fromEdges {
		seenEdge[key] = true
		edgeKeys = append(edgeKeys, key)
	}
	for key := range toEdges {
		if !seenEdge[key] {
			edgeKeys = append(edgeKeys, key)
		}
	}
	sort.Strings(edgeKeys)
	for _, key := range edgeKeys {
		_, hadBefore := fromEdges[key]
		after, hadAfter := toEdges[key]
		switch {
		case !hadBefore && hadAfter:
			result.Diff = append(result.Diff, diffEntry{Kind: "edge_added", Field: key, After: map[string]any{"from": after.From, "to": after.To, "type": after.Type}})
		case hadBefore && !hadAfter:
			before := fromEdges[key]
			result.Diff = append(result.Diff, diffEntry{Kind: "edge_removed", Field: key, Before: map[string]any{"from": before.From, "to": before.To, "type": before.Type}})
		}
	}
	// Budget comparison.
	beforeBudget := []diffEntry{}
	addBudget := func(field string, before, after any, changed bool) {
		if changed {
			beforeBudget = append(beforeBudget, diffEntry{Kind: "budget_changed", Field: field, Before: before, After: after})
		}
	}
	addBudget("max_task_attempts", fromGraph.Budget.MaxTaskAttempts, toGraph.Budget.MaxTaskAttempts, fromGraph.Budget.MaxTaskAttempts != toGraph.Budget.MaxTaskAttempts)
	addBudget("max_duration_seconds", fromGraph.Budget.MaxDurationSec, toGraph.Budget.MaxDurationSec, fromGraph.Budget.MaxDurationSec != toGraph.Budget.MaxDurationSec)
	addBudget("max_tokens", fromGraph.Budget.MaxTokens, toGraph.Budget.MaxTokens, fromGraph.Budget.MaxTokens != toGraph.Budget.MaxTokens)
	addBudget("max_cost_usd", fromGraph.Budget.MaxCostUSD, toGraph.Budget.MaxCostUSD, fromGraph.Budget.MaxCostUSD != toGraph.Budget.MaxCostUSD)
	addBudget("max_sandbox_seconds", fromGraph.Budget.MaxSandboxSeconds, toGraph.Budget.MaxSandboxSeconds, fromGraph.Budget.MaxSandboxSeconds != toGraph.Budget.MaxSandboxSeconds)
	addBudget("max_download_bytes", fromGraph.Budget.MaxDownloadBytes, toGraph.Budget.MaxDownloadBytes, fromGraph.Budget.MaxDownloadBytes != toGraph.Budget.MaxDownloadBytes)
	addBudget("policy", fromGraph.Budget.Policy, toGraph.Budget.Policy, fromGraph.Budget.Policy != toGraph.Budget.Policy)
	result.Diff = append(result.Diff, beforeBudget...)
	return result
}

func diffNodeFields(nodeID string, before, after *models.TaskNode) []diffEntry {
	entries := []diffEntry{}
	add := func(field string, oldValue, newValue any, changed bool) {
		if changed {
			entries = append(entries, diffEntry{Kind: "node_modified", NodeID: nodeID, Field: field, Before: oldValue, After: newValue})
		}
	}
	add("name", before.Name, after.Name, before.Name != after.Name)
	add("type", before.Type, after.Type, before.Type != after.Type)
	add("description", before.Description, after.Description, before.Description != after.Description)
	add("assigned_to", before.AssignedTo, after.AssignedTo, before.AssignedTo != after.AssignedTo)
	add("timeout_seconds", before.TimeoutSeconds, after.TimeoutSeconds, before.TimeoutSeconds != after.TimeoutSeconds)
	add("retry_limit", before.RetryLimit, after.RetryLimit, before.RetryLimit != after.RetryLimit)
	add("priority", before.Priority, after.Priority, before.Priority != after.Priority)
	add("parallelizable", before.Parallelizable, after.Parallelizable, before.Parallelizable != after.Parallelizable)
	add("enabled", before.IsEnabled(), after.IsEnabled(), before.IsEnabled() != after.IsEnabled())
	beforeDeps := append([]string(nil), before.Dependencies...)
	afterDeps := append([]string(nil), after.Dependencies...)
	sort.Strings(beforeDeps)
	sort.Strings(afterDeps)
	add("dependencies", beforeDeps, afterDeps, strings.Join(beforeDeps, ",") != strings.Join(afterDeps, ","))
	beforeRequired := append([]string(nil), before.RequiredArtifacts...)
	afterRequired := append([]string(nil), after.RequiredArtifacts...)
	sort.Strings(beforeRequired)
	sort.Strings(afterRequired)
	add("required_artifacts", beforeRequired, afterRequired, strings.Join(beforeRequired, ",") != strings.Join(afterRequired, ","))
	add("inputs", before.Inputs, after.Inputs, !inputsEqual(before.Inputs, after.Inputs))
	return entries
}

func inputsEqual(left, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok {
			return false
		}
		if !valueEqual(value, other) {
			return false
		}
	}
	return true
}

func valueEqual(left, right any) bool {
	switch l := left.(type) {
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok {
			return false
		}
		return inputsEqual(l, r)
	case []any:
		r, ok := right.([]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for i := range l {
			if !valueEqual(l[i], r[i]) {
				return false
			}
		}
		return true
	default:
		return left == right
	}
}

// diffSummary renders a short human-readable list for PlanRevision.DiffSummary.
func diffSummary(diff planDiff) []string {
	out := make([]string, 0, len(diff.Diff))
	for _, entry := range diff.Diff {
		switch entry.Kind {
		case "node_added":
			out = append(out, "新增节点 "+entry.NodeID)
		case "node_removed":
			out = append(out, "删除节点 "+entry.NodeID)
		case "edge_added":
			out = append(out, "新增依赖 "+entry.Field)
		case "edge_removed":
			out = append(out, "删除依赖 "+entry.Field)
		case "node_modified":
			out = append(out, "修改节点 "+entry.NodeID+" 字段 "+entry.Field)
		case "budget_changed":
			out = append(out, "修改预算 "+entry.Field)
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestNormalizeArtifactContractsAddsRuntimeAndImage(t *testing.T) {
	nodes := []*models.TaskNode{
		{ID: "gen", Type: "generate_code", OutputArtifacts: []string{"generated_code"}},
		{ID: "prep", Type: "prepare_runtime", OutputArtifacts: []string{"runtime_session"}},
		{ID: "install", Type: "install_dependencies", OutputArtifacts: []string{"prepared_runtime"}},
		{ID: "exec", Type: "execute_code", OutputArtifacts: []string{"execution_result"}},
		{ID: "plot", Type: "render_plot", OutputArtifacts: []string{"plot_image"}},
	}
	normalizeArtifactContracts(nodes)

	if !containsArtifact(nodes[3].OutputArtifacts, "execution_image") {
		t.Fatalf("execute_code must publish execution_image: %v", nodes[3].OutputArtifacts)
	}
	if !containsArtifact(nodes[4].RequiredArtifacts, "prepared_runtime") {
		t.Fatalf("render_plot must require prepared_runtime: %v", nodes[4].RequiredArtifacts)
	}
	if !containsArtifact(nodes[4].RequiredArtifacts, "execution_image") {
		t.Fatalf("render_plot must require execution_image: %v", nodes[4].RequiredArtifacts)
	}
}

func TestNormalizeArtifactContractsSkipsMissingProducers(t *testing.T) {
	nodes := []*models.TaskNode{
		{ID: "plot", Type: "render_plot", OutputArtifacts: []string{"plot_image"}},
	}
	normalizeArtifactContracts(nodes)
	if containsArtifact(nodes[0].RequiredArtifacts, "prepared_runtime") {
		t.Fatalf("render_plot must not require an artifact with no producer")
	}
}

func TestNamespaceDuplicateArtifactsRewritesBranchKeys(t *testing.T) {
	branchA := &models.TaskNode{ID: "a", Name: "Generate LangChain Benchmark Code", Type: "generate_code", OutputArtifacts: []string{"generated_code"}}
	branchB := &models.TaskNode{ID: "b", Name: "Generate LlamaIndex Benchmark Code", Type: "generate_code", OutputArtifacts: []string{"generated_code"}}
	runA := &models.TaskNode{ID: "ra", Type: "execute_code", Dependencies: []string{"a"}, RequiredArtifacts: []string{"generated_code"}, OutputArtifacts: []string{"metrics_a"}}
	runB := &models.TaskNode{ID: "rb", Type: "execute_code", Dependencies: []string{"b"}, RequiredArtifacts: []string{"generated_code"}, OutputArtifacts: []string{"metrics_b"}}
	nodes := []*models.TaskNode{branchA, branchB, runA, runB}
	namespaceDuplicateArtifacts(nodes)

	if branchA.OutputArtifacts[0] == branchB.OutputArtifacts[0] {
		t.Fatalf("branch artifact keys must be unique: %v vs %v", branchA.OutputArtifacts, branchB.OutputArtifacts)
	}
	if runA.RequiredArtifacts[0] != branchA.OutputArtifacts[0] {
		t.Fatalf("runA must consume its own branch artifact, got %v", runA.RequiredArtifacts)
	}
	if runB.RequiredArtifacts[0] != branchB.OutputArtifacts[0] {
		t.Fatalf("runB must consume its own branch artifact, got %v", runB.RequiredArtifacts)
	}
}

func TestMergeConsecutiveGeneralProcess(t *testing.T) {
	first := &models.TaskNode{ID: "g1", Type: "general_process", OutputArtifacts: []string{"intent_scope"}}
	second := &models.TaskNode{ID: "g2", Type: "general_process", Dependencies: []string{"g1"}, OutputArtifacts: []string{"final_answer"}}
	third := &models.TaskNode{ID: "g3", Type: "general_synthesis", Dependencies: []string{"g2"}, OutputArtifacts: []string{"general_response"}}
	out := mergeConsecutiveGeneralProcess([]*models.TaskNode{first, second, third})
	if len(out) != 2 {
		t.Fatalf("expected merged 2 nodes, got %d", len(out))
	}
	if !containsArtifact(out[0].OutputArtifacts, "final_answer") {
		t.Fatalf("merged node lost outputs: %v", out[0].OutputArtifacts)
	}
	if len(out[1].Dependencies) != 1 || out[1].Dependencies[0] != "g1" {
		t.Fatalf("downstream dependency was not rewired: %v", out[1].Dependencies)
	}
}

func TestPruneOutputlessNodes(t *testing.T) {
	producer := &models.TaskNode{ID: "p", Type: "generate_code", OutputArtifacts: []string{"generated_code"}}
	empty := &models.TaskNode{ID: "e", Type: "general_process", Dependencies: []string{"p"}}
	consumer := &models.TaskNode{ID: "c", Type: "verify_result", Dependencies: []string{"e"}, OutputArtifacts: []string{"verification_report"}}
	out := pruneOutputlessNodes([]*models.TaskNode{producer, empty, consumer})
	if len(out) != 2 {
		t.Fatalf("expected outputless node pruned, got %d", len(out))
	}
	if len(out[1].Dependencies) != 1 || out[1].Dependencies[0] != "p" {
		t.Fatalf("consumer dependency was not rewired: %v", out[1].Dependencies)
	}
}

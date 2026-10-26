package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/models"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeDatasetShapeDetectsNodeLabelTable(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "email-Eu-core-department-labels.txt", "0 0\n1 1\n2 2\n3 0\n4 1\n5 2\n6 0\n7 1\n8 2\n9 0\n10 1\n11 2\n")
	profiles := probeWorkspaceDatasets(dir)
	if len(profiles) != 1 {
		t.Fatalf("profiles=%#v", profiles)
	}
	if profiles[0].Shape != "node_label" {
		t.Fatalf("shape=%s, want node_label (profile=%#v)", profiles[0].Shape, profiles[0])
	}
	if profiles[0].Col0UniqueRatio != 1.0 || profiles[0].Col1Unique > 100 {
		t.Fatalf("unexpected profile: %#v", profiles[0])
	}
}

func TestProbeDatasetShapeDetectsEdgeList(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "karate.edges", "0 1\n0 2\n1 2\n2 3\n3 4\n4 0\n1 3\n")
	profiles := probeWorkspaceDatasets(dir)
	if len(profiles) != 1 || profiles[0].Shape != "edge_list" {
		t.Fatalf("profiles=%#v, want edge_list", profiles)
	}
}

func TestDatasetShapeGateBlocksLabelTableUsedAsEdgeList(t *testing.T) {
	dir := t.TempDir()
	label := writeTestFile(t, dir, "email-Eu-core-department-labels.txt", "0 0\n1 1\n2 2\n3 0\n4 1\n5 2\n6 0\n7 1\n8 2\n9 0\n10 1\n11 2\n")
	code := "import networkx as nx\nG = nx.read_edgelist('email-Eu-core-department-labels.txt')\nprint(nx.community.louvain_communities(G))\n"
	codePath := writeTestFile(t, dir, "main.py", code)
	task := &models.Task{Name: "Execute And Debug Baseline", Description: "复现 Louvain 社区发现论文"}
	profiles := probeWorkspaceDatasets(dir)
	missing := datasetShapeGate(dir, codePath, task, profiles)
	if missing == nil {
		t.Fatalf("expected a blocked dataset requirement; label=%s code=%s", label, codePath)
	}
	if missing.Kind != "dataset" || missing.Remediation == "" {
		t.Fatalf("missing requirement is not actionable: %#v", missing)
	}
}

func TestDatasetShapeGateAllowsRealEdgeList(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "karate.edges", "0 1\n0 2\n1 2\n2 3\n3 4\n4 0\n1 3\n")
	code := "import networkx as nx\nG = nx.read_edgelist('karate.edges')\nprint(nx.community.louvain_communities(G))\n"
	codePath := writeTestFile(t, dir, "main.py", code)
	task := &models.Task{Name: "Execute And Debug Baseline", Description: "复现 Louvain 社区发现论文"}
	if missing := datasetShapeGate(dir, codePath, task, probeWorkspaceDatasets(dir)); missing != nil {
		t.Fatalf("edge list must pass: %#v", missing)
	}
}

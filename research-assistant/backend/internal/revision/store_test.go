package revision

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func testGraph(id string, nodes int) *models.PlanGraph {
	graph := &models.PlanGraph{ID: id, UserIntent: "intent " + id, Nodes: []*models.TaskNode{}, CreatedAt: time.Now().UTC()}
	for i := 0; i < nodes; i++ {
		graph.Nodes = append(graph.Nodes, &models.TaskNode{ID: id + "-n" + string(rune('a'+i)), Type: "generate_code", Status: models.StatusPending})
	}
	return graph
}

func TestMemoryStoreAppendsAndSnapshots(t *testing.T) {
	store := NewMemoryStore()
	first, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 1), CreatedBy: "u1", Reason: "initial"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.ParentRevision != 0 || first.Hash == "" {
		t.Fatalf("unexpected first revision: %+v", first)
	}
	// Mutating the input graph must not mutate the stored snapshot.
	secondGraph := testGraph("p1", 2)
	second, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: secondGraph, CreatedBy: "u1", Reason: "change"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.ParentRevision != 1 {
		t.Fatalf("unexpected second revision: %+v", second)
	}
	secondGraph.Nodes[0].Name = "mutated"
	stored, err := store.Get("p1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Graph.Nodes[0].Name == "mutated" {
		t.Fatal("revision graph must be a deep copy")
	}
	one, _ := store.Get("p1", 1)
	if one.Graph.Nodes[0].Name == "mutated" {
		t.Fatal("revision 1 must be unchanged")
	}
}

func TestMemoryStoreRejectsStaleBase(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 1)}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 2)}, 1); err != nil {
		t.Fatal(err)
	}
	_, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 3)}, 1)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 {
		t.Fatalf("conflict must report current revision 2: %+v", err)
	}
}

func TestMemoryStoreConcurrentCreates(t *testing.T) {
	store := NewMemoryStore()
	var wg sync.WaitGroup
	var mu sync.Mutex
	revisions := map[int]bool{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			rev, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 1)}, 0)
			if err != nil {
				return
			}
			mu.Lock()
			revisions[rev.Revision] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(revisions) != 20 {
		t.Fatalf("concurrent creates produced %d distinct revisions, want 20", len(revisions))
	}
	for i := 1; i <= 20; i++ {
		if !revisions[i] {
			t.Fatalf("missing revision %d", i)
		}
	}
}

func TestFileStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "revisions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 2), CreatedBy: "u1"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateStatus("p1", 1, models.RevisionApproved, "u2", nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := reopened.Latest("p1")
	if err != nil {
		t.Fatal(err)
	}
	if rev.Revision != 1 || rev.Status != models.RevisionApproved || rev.ApprovedBy != "u2" {
		t.Fatalf("unexpected persisted revision: %+v", rev)
	}
	if rev.Graph == nil || len(rev.Graph.Nodes) != 2 {
		t.Fatalf("graph must persist: %+v", rev.Graph)
	}
}

func TestFileStoreDeletePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "revisions.json")
	store, _ := NewFileStore(path)
	_, _ = store.Create(&models.PlanRevision{PlanID: "p1", Graph: testGraph("p1", 1)}, 0)
	if err := store.DeletePlan("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest("p1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// The deletion must persist.
	reopened, _ := NewFileStore(path)
	if _, err := reopened.Latest("p1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deletion must persist, got %v", err)
	}
}

func TestRevisionGraphHashStable(t *testing.T) {
	graph := testGraph("p1", 2)
	first := models.RevisionGraphHash(graph)
	raw, _ := json.Marshal(graph)
	var restored models.PlanGraph
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if second := models.RevisionGraphHash(&restored); first != second {
		t.Fatalf("hash not stable: %s vs %s", first, second)
	}
}

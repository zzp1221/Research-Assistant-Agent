package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
)

func repairFixture() (*models.PlanGraph, *models.TaskNode, models.ActionLedgerEntry) {
	return &models.PlanGraph{ID: "plan", OwnerID: "owner", ProjectID: "project", SessionID: "session"}, &models.TaskNode{ID: "task", Name: "repair", AssignedTo: "coder", RunCount: 2}, models.ActionLedgerEntry{ID: "action", Kind: "repair", Action: "apply verified patch", BeforeStateHash: "before", AfterStateHash: "after", EnvironmentHash: "env", ProtocolHash: "protocol", BeforeEvidence: []string{"failure-log"}, AfterEvidence: []string{"verification-log"}, ArtifactRefs: []string{"patch", "verification-log"}, ExecutionID: "exec", Verified: true}
}

func TestRetryWithoutActionIsOnlyEpisodeAndCurrentErrorWins(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plan, task, _ := repairFixture()
	if err := RecordTask(ctx, s, plan, task, &models.TaskExecutionResult{Status: models.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	records, _, _ := s.List(ctx, Query{OwnerID: "owner", AllScopes: true, IncludePending: true})
	for _, rec := range records {
		if rec.Kind == KindProcedure {
			t.Fatal("retry count promoted an unobserved repair")
		}
	}
	task.Error = "old stale error"
	if err := RecordTask(ctx, s, plan, task, &models.TaskExecutionResult{Status: models.StatusFailed, Error: "current execution error"}); err != nil {
		t.Fatal(err)
	}
	records, _, _ = s.List(ctx, Query{OwnerID: "owner", AllScopes: true, IncludePending: true})
	found := false
	for _, rec := range records {
		if strings.Contains(rec.Content, "current execution error") {
			found = true
		}
		if strings.Contains(rec.Content, "old stale error") {
			t.Fatal("task error replaced current execution error")
		}
	}
	if !found {
		t.Fatal("current failure missing")
	}
}

func TestVerifiedProcedureRequiresCompatibilityAndExplicitPromotion(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plan, task, entry := repairFixture()
	if err := RecordRepairProcedures(ctx, s, plan, task, []models.ActionLedgerEntry{entry}); err != nil {
		t.Fatal(err)
	}
	records, _, _ := s.List(ctx, Query{OwnerID: "owner", AllScopes: true, IncludePending: true})
	if len(records) != 1 || records[0].Status != StatusPending || records[0].Trust != TrustInferred {
		t.Fatalf("candidate promoted without flag: %#v", records)
	}
	t.Setenv("MEMORY_PROCEDURE_PROMOTION", "true")
	entry.ID = "second-action"
	entry.ExecutionID = "second-exec"
	if err := RecordRepairProcedures(ctx, s, plan, task, []models.ActionLedgerEntry{entry}); err != nil {
		t.Fatal(err)
	}
	query := Query{OwnerID: "owner", ProjectID: "project", EnvironmentHash: "env", ProtocolHash: "protocol", Text: "verified"}
	hits, _ := s.Search(ctx, query)
	if len(hits) != 1 {
		t.Fatalf("compatible promoted case unavailable: %#v", hits)
	}
	query.EnvironmentHash = "different-env"
	hits, _ = s.Search(ctx, query)
	if len(hits) != 0 {
		t.Fatal("incompatible procedure leaked")
	}
	query.EnvironmentHash = "env"
	query.OwnerID = "another-owner"
	hits, _ = s.Search(ctx, query)
	if len(hits) != 0 {
		t.Fatal("cross-owner procedure leaked")
	}
	query.OwnerID = "owner"
	hits, _ = s.Search(ctx, query)
	id := hits[0].Record.ID
	if err := RecordProcedureUse(ctx, s, "owner", id, "retrieved", "adoption", nil); err != nil {
		t.Fatal(err)
	}
	failed := false
	if err := RecordProcedureUse(ctx, s, "owner", id, "verified", "adoption", &failed); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Get(ctx, "owner", id)
	if saved.Status != StatusPending {
		t.Fatal("failed reuse left procedure active")
	}
}

func TestIncompleteOrUnchangedRepairNeverCreatesProcedure(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plan, task, entry := repairFixture()
	entry.AfterStateHash = entry.BeforeStateHash
	if err := RecordRepairProcedures(ctx, s, plan, task, []models.ActionLedgerEntry{entry}); err != nil {
		t.Fatal(err)
	}
	_, count, _ := s.List(ctx, Query{OwnerID: "owner", AllScopes: true, IncludePending: true})
	if count != 0 {
		t.Fatal("unchanged action became procedure")
	}
}

func TestExplicitAdoptionTracksActualRepairThroughVerification(t *testing.T) {
	t.Setenv("MEMORY_PROCEDURE_PROMOTION", "true")
	ctx := context.Background()
	store := NewMemoryStore()
	plan, task, entry := repairFixture()
	if err := RecordRepairProcedures(ctx, store, plan, task, []models.ActionLedgerEntry{entry}); err != nil {
		t.Fatal(err)
	}
	records, _, _ := store.List(ctx, Query{OwnerID: "owner"})
	id := records[0].ID
	_ = RecordProcedureUse(ctx, store, "owner", id, "retrieved", "next", nil)
	if got := AdoptProcedures(ctx, store, "owner", []string{id}, []string{}, "env", "protocol", "next"); len(got) != 0 {
		t.Fatal("unretrieved procedure adopted")
	}
	adopted := AdoptProcedures(ctx, store, "owner", []string{id}, []string{id}, "env", "protocol", "next")
	if len(adopted) != 1 {
		t.Fatal("compatible explicit adoption missing")
	}
	controller := recovery.New(models.RecoveryState{ChainID: "next"}, nil)
	runCtx := recovery.WithCompatibility(recovery.WithController(ctx, controller), recovery.Compatibility{EnvironmentHash: "env", ProtocolHash: "protocol", ExecutionID: "next", ProcedureIDs: adopted})
	if err := recovery.RecordVerifiedRepair(runCtx, "old code", "fixed code", "failed", "passed"); err != nil {
		t.Fatal(err)
	}
	if err := RecordProcedureActions(ctx, store, "owner", controller.Snapshot().Ledger); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.Get(ctx, "owner", id)
	raw, _ := json.Marshal(saved.Attributes["uses"])
	for _, stage := range []string{"retrieved", "adopted", "executed", "verified"} {
		if !strings.Contains(string(raw), stage) {
			t.Fatal("missing actual reuse stage " + stage)
		}
	}
}

type simultaneousProcedureStore struct {
	Store
	arrived chan struct{}
	release chan struct{}
	count   atomic.Int32
	limit   int32
}

func (s *simultaneousProcedureStore) Update(ctx context.Context, owner, id string, patch Patch) (Record, error) {
	if s.count.Add(1) <= s.limit {
		s.arrived <- struct{}{}
		<-s.release
	}
	return s.Store.Update(ctx, owner, id, patch)
}

func TestConcurrentProcedureUsesRemainDurableAndInvalidateAtomically(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()
	rec, err := store.Put(ctx, Record{ID: "procedure", Kind: KindProcedure, Scope: Scope{OwnerID: "owner"}, Title: "verified repair", Content: "repair evidence", Status: StatusActive, Attributes: map[string]any{"environment_hash": "env", "protocol_hash": "protocol"}})
	if err != nil {
		t.Fatal(err)
	}
	const distinct = 8
	const calls = distinct * 2
	concurrent := &simultaneousProcedureStore{Store: store, arrived: make(chan struct{}, calls), release: make(chan struct{}), limit: calls}
	errors := make(chan error, calls)
	for i := 0; i < calls; i++ {
		go func(index int) {
			stage := "retrieved"
			var successful *bool
			if index%distinct == 0 {
				stage = "verified"
				failed := false
				successful = &failed
			}
			errors <- RecordProcedureUse(ctx, concurrent, "owner", rec.ID, stage, fmt.Sprintf("exec-%d", index%distinct), successful)
		}(i)
	}
	// All callers reach the write together. A Get/replace implementation has
	// already read the same old history and deterministically loses entries.
	for i := 0; i < calls; i++ {
		<-concurrent.arrived
	}
	close(concurrent.release)
	for i := 0; i < calls; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = nil
	reopened, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Get(ctx, "owner", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	var uses []map[string]any
	raw, _ := json.Marshal(saved.Attributes["uses"])
	if err := json.Unmarshal(raw, &uses); err != nil {
		t.Fatal(err)
	}
	if len(uses) != distinct || saved.Version != rec.Version+distinct || saved.Status != StatusPending {
		t.Fatalf("concurrent uses were lost, duplicated, or separated from invalidation: version=%d status=%s uses=%s", saved.Version, saved.Status, raw)
	}
	if saved.Attributes["environment_hash"] != "env" || saved.Attributes["protocol_hash"] != "protocol" {
		t.Fatal("use recording replaced compatibility attributes")
	}
	for _, use := range uses {
		if use["execution_id"] == "exec-0" && (use["result"] != false || use["invalidated"] != true) {
			t.Fatalf("failed verification lost its invalidation evidence: %#v", use)
		}
	}
}

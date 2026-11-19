package store

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

type isolationStoreCase struct {
	name string
	new  func(*testing.T) (PlanStore, func() PlanStore)
}

func isolationStores() []isolationStoreCase {
	return []isolationStoreCase{
		{name: "memory", new: func(*testing.T) (PlanStore, func() PlanStore) {
			return NewMemoryPlanStore(), nil
		}},
		{name: "file", new: func(t *testing.T) (PlanStore, func() PlanStore) {
			path := filepath.Join(t.TempDir(), "plans.jsonl")
			open := func() PlanStore {
				store, err := NewFilePlanStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return store
			}
			return open(), open
		}},
	}
}

func TestPlanStoreIsolationAcrossEntrypoints(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, reopen := storeCase.new(t)
			plan := richIsolationPlan("isolation")
			want := isolationJSON(t, plan)
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			mutateIsolationPlan(plan)
			assertIsolationState(t, store, "isolation", want)
			loaded, err := store.GetPlan("isolation")
			if err != nil {
				t.Fatal(err)
			}
			mutateIsolationPlan(loaded)
			assertIsolationState(t, store, "isolation", want)
			plans, err := store.ListPlans()
			if err != nil || len(plans) != 1 {
				t.Fatalf("list plans: %d, %v", len(plans), err)
			}
			mutateIsolationPlan(plans[0])
			assertIsolationState(t, store, "isolation", want)
			if reopen != nil {
				assertIsolationState(t, reopen(), "isolation", want)
			}
		})
	}
}

func TestPlanStoreCallbackIsolationAndNestedPersistence(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, reopen := storeCase.new(t)
			plan := richIsolationPlan("callback")
			want := isolationJSON(t, plan)
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			callbackErr := errors.New("reject candidate")
			if err := store.UpdatePlan(plan.ID, func(working *models.PlanGraph) error {
				mutateIsolationPlan(working)
				return callbackErr
			}); !errors.Is(err, callbackErr) {
				t.Fatalf("callback error: %v", err)
			}
			assertIsolationState(t, store, plan.ID, want)
			if reopen != nil {
				assertIsolationState(t, reopen(), plan.ID, want)
			}

			var retained *models.PlanGraph
			external := map[string]any{"scores": []int{2}}
			if err := store.UpdatePlan(plan.ID, func(working *models.PlanGraph) error {
				retained = working
				working.Nodes[0].Inputs["metrics"].(map[string]any)["score"] = 2
				working.Nodes[0].Metadata["external"] = external
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			committed, err := store.GetPlan(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			want = isolationJSON(t, committed)
			mutateIsolationPlan(retained)
			external["scores"].([]int)[0] = 999
			assertIsolationState(t, store, plan.ID, want)
			if reopen != nil {
				assertIsolationState(t, reopen(), plan.ID, want)
			}

			// No scalar or timestamp is changed: nested-only updates must still
			// be persisted, rather than being mistaken for a no-op.
			if err := store.UpdatePlan(plan.ID, func(working *models.PlanGraph) error {
				working.Nodes[0].Inputs["metrics"].(map[string]any)["score"] = 3
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			committed, _ = store.GetPlan(plan.ID)
			if committed.Nodes[0].Inputs["metrics"].(map[string]any)["score"] != 3 {
				t.Fatal("nested update was not committed")
			}
			if reopen != nil {
				assertIsolationState(t, reopen(), plan.ID, isolationJSON(t, committed))
			}
		})
	}
}

func TestPlanStoreNestedOnlyMutationsPersist(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			for _, mutation := range []struct {
				name   string
				value  func() any
				mutate func(any)
			}{
				{
					name:   "map",
					value:  func() any { return map[string]any{"score": 1} },
					mutate: func(value any) { value.(map[string]any)["score"] = 2 },
				},
				{
					name:   "slice",
					value:  func() any { return []int{1} },
					mutate: func(value any) { value.([]int)[0] = 2 },
				},
				{
					name: "pointer",
					value: func() any {
						number := 1
						return &number
					},
					mutate: func(value any) { *value.(*int) = 2 },
				},
				{
					name:   "raw_message",
					value:  func() any { return json.RawMessage(`[1]`) },
					mutate: func(value any) { value.(json.RawMessage)[1] = '2' },
				},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					store, reopen := storeCase.new(t)
					plan := testPlan("nested-only-" + mutation.name)
					plan.Nodes[0].Inputs = map[string]any{"value": mutation.value()}
					before := isolationJSON(t, plan)
					if err := store.SavePlan(plan); err != nil {
						t.Fatal(err)
					}
					if err := store.UpdatePlan(plan.ID, func(candidate *models.PlanGraph) error {
						// Change only the referenced value. In particular, do not
						// update a timestamp or another scalar that masks aliasing.
						mutation.mutate(candidate.Nodes[0].Inputs["value"])
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					mutation.mutate(plan.Nodes[0].Inputs["value"])
					want := isolationJSON(t, plan)
					if reflect.DeepEqual(before, want) {
						t.Fatal("test mutation did not change serialized content")
					}
					assertIsolationState(t, store, plan.ID, want)
					if reopen != nil {
						assertIsolationState(t, reopen(), plan.ID, want)
					}
				})
			}
		})
	}
}

func TestPlanStoreRejectsInvalidCandidatesWithoutMutation(t *testing.T) {
	invalidValues := map[string]func() any{
		"NaN":      func() any { return math.NaN() },
		"infinity": func() any { return math.Inf(1) },
		"function": func() any { return func() {} },
		"channel":  func() any { return make(chan int) },
		"raw JSON": func() any { return json.RawMessage(`{broken`) },
		"cycle": func() any {
			value := map[string]any{}
			value["self"] = value
			return value
		},
	}
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, reopen := storeCase.new(t)
			plan := richIsolationPlan("invalid")
			want := isolationJSON(t, plan)
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			for name, value := range invalidValues {
				t.Run(name, func(t *testing.T) {
					invalid := richIsolationPlan(plan.ID)
					invalid.Nodes[0].Inputs["invalid"] = value()
					if err := store.SavePlan(invalid); err == nil {
						t.Fatal("invalid save succeeded")
					}
					assertIsolationState(t, store, plan.ID, want)
					if err := store.UpdatePlan(plan.ID, func(working *models.PlanGraph) error {
						working.Title = "must not leak"
						working.Nodes[0].Inputs["invalid"] = value()
						return nil
					}); err == nil {
						t.Fatal("invalid update succeeded")
					}
					assertIsolationState(t, store, plan.ID, want)
					if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, Payload: map[string]any{"invalid": value()}}); err == nil {
						t.Fatal("invalid event succeeded")
					}
					events, err := store.ListEvents(plan.ID)
					if err != nil || len(events) != 0 {
						t.Fatalf("invalid event changed state: %v, %v", events, err)
					}
				})
			}
			if err := store.UpdatePlan(plan.ID, func(working *models.PlanGraph) error {
				working.ID = "different-id"
				return nil
			}); err == nil {
				t.Fatal("plan identity change succeeded")
			}
			assertIsolationState(t, store, plan.ID, want)
			if reopen != nil {
				assertIsolationState(t, reopen(), plan.ID, want)
			}
		})
	}
}

func TestPlanStoreEventIsolationAndLifecycleSequence(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, reopen := storeCase.new(t)
			plan := richIsolationPlan("events")
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			input := models.PlanEvent{PlanID: plan.ID, Payload: map[string]any{
				"nested": map[string]any{"score": 1}, "bytes": json.RawMessage(`[1]`),
			}}
			returned, err := store.AppendEventReturning(plan.ID, input)
			if err != nil || returned.Seq != 1 {
				t.Fatalf("append: %v, %v", returned, err)
			}
			input.Payload["nested"].(map[string]any)["score"] = 999
			returned.Payload["nested"].(map[string]any)["score"] = 998
			returned.Payload["bytes"].(json.RawMessage)[1] = '9'
			listed, err := store.ListEvents(plan.ID)
			if err != nil || len(listed) != 1 || listed[0].Payload["nested"].(map[string]any)["score"] != 1 {
				t.Fatalf("event copy leaked: %v, %v", listed, err)
			}
			listed[0].Payload["nested"].(map[string]any)["score"] = 997
			incremental, _ := store.ListEventsAfter(plan.ID, 0)
			if incremental[0].Payload["nested"].(map[string]any)["score"] != 1 {
				t.Fatal("list output aliases event storage")
			}
			incremental[0].Payload["nested"].(map[string]any)["score"] = 996
			for _, bad := range []models.PlanEvent{
				{PlanID: "other"}, {PlanID: plan.ID, Seq: 1}, {PlanID: plan.ID, Seq: -1},
			} {
				if err := store.AppendEvent(plan.ID, bad); err == nil {
					t.Fatalf("invalid event accepted: %+v", bad)
				}
			}
			if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, Seq: 10}); err != nil {
				t.Fatal(err)
			}
			automatic, err := store.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID})
			if err != nil || automatic.Seq != 11 {
				t.Fatalf("automatic seq after explicit seq: %d, %v", automatic.Seq, err)
			}
			if err := store.DeletePlan(plan.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
				t.Fatalf("deleted lookup: %v", err)
			}
			if reopen != nil {
				store = reopen()
			}
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			listed, err = store.ListEvents(plan.ID)
			if err != nil || len(listed) != 0 {
				t.Fatal("recreated plan inherited previous lifecycle events")
			}
			automatic, err = store.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID})
			if err != nil || automatic.Seq != 12 {
				t.Fatalf("recreated plan seq: %d, %v", automatic.Seq, err)
			}
			if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, Seq: math.MaxInt64}); err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID}); err == nil {
				t.Fatal("sequence overflow accepted")
			}
		})
	}
}

func TestPlanStoreSummaryIsolation(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, _ := storeCase.new(t)
			plan := richIsolationPlan("summary")
			want := isolationJSON(t, plan)
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			source := store.(PlanSummarySource)
			summaries, err := source.ListPlanSummaries()
			if err != nil || len(summaries) != 1 {
				t.Fatalf("summaries: %d, %v", len(summaries), err)
			}
			before := summaries[0]
			*before.FinishedAt = before.FinishedAt.Add(time.Hour)
			*before.Usage.StartedAt = before.Usage.StartedAt.Add(time.Hour)
			*before.Usage.FinishedAt = before.Usage.FinishedAt.Add(time.Hour)
			*before.Usage.ApprovalWaitingSince = before.Usage.ApprovalWaitingSince.Add(time.Hour)
			before.Usage.BudgetWarnings[0] = "mutated"
			*before.Review.ReviewedAt = before.Review.ReviewedAt.Add(time.Hour)
			assertIsolationState(t, store, plan.ID, want)
			after, _ := source.ListPlanSummaries()
			if after[0].Usage.BudgetWarnings[0] == "mutated" || !after[0].FinishedAt.Equal(*plan.Usage.FinishedAt) {
				t.Fatal("summary outputs alias each other")
			}
		})
	}
}

func TestPlanStoreConcurrentReturnedCopies(t *testing.T) {
	for _, storeCase := range isolationStores() {
		t.Run(storeCase.name, func(t *testing.T) {
			store, _ := storeCase.new(t)
			plan := richIsolationPlan("concurrent")
			if err := store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			failures := make(chan error, 3)
			var workers sync.WaitGroup
			for worker := 0; worker < 3; worker++ {
				workers.Add(1)
				go func(writer bool) {
					defer workers.Done()
					<-start
					for iteration := 0; iteration < 15; iteration++ {
						if writer {
							if err := store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
								current.Nodes[0].Inputs["metrics"].(map[string]any)["score"] = iteration
								return nil
							}); err != nil {
								failures <- err
								return
							}
						} else {
							copy, err := store.GetPlan(plan.ID)
							if err != nil {
								failures <- err
								return
							}
							mutateIsolationPlan(copy)
						}
					}
				}(worker == 0)
			}
			close(start)
			workers.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			committed, err := store.GetPlan(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if committed.Nodes[0].Inputs["metrics"].(map[string]any)["score"] != 14 || committed.Title != plan.Title {
				t.Fatal("returned copies changed the committed plan")
			}
		})
	}
}

func isolationJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func assertIsolationState(t *testing.T, store PlanStore, id string, want any) {
	t.Helper()
	got, err := store.GetPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(isolationJSON(t, got), want) {
		t.Fatal("committed plan changed outside a successful update")
	}
}

func richIsolationPlan(id string) *models.PlanGraph {
	now := time.Date(2026, 11, 19, 1, 2, 3, 4, time.UTC)
	enabled, optional := true, true
	verification := &models.Verification{Issues: []string{"original"}, Images: []models.ImageVerification{{Evidence: []string{"original"}}}}
	return &models.PlanGraph{
		ID: id, Title: "original", ArchivedAt: &now, PausedAt: &now,
		Status: models.StatusPending, CreatedAt: now, UpdatedAt: now,
		Approval:            models.ApprovalState{ApprovedAt: &now},
		Usage:               models.RunUsage{StartedAt: &now, FinishedAt: &now, ApprovalWaitingSince: &now, BudgetWarnings: []string{"original"}},
		Review:              models.ReviewState{ReviewedAt: &now},
		MissingRequirements: []models.MissingRequirement{{Name: "original"}},
		Context: &models.ResearchContext{
			Attachments: []models.AttachmentRef{{Name: "original"}}, Documents: []models.DocumentRef{{DocumentID: "original"}},
			Paper:    &models.PaperMetadata{Authors: []string{"original"}, Warnings: []string{"original"}},
			Entities: map[string]any{"nested": []string{"original"}}, Constraints: map[string]any{"nested": map[string]string{"value": "original"}},
			MemoryRefs: []string{"original"},
		},
		Meta: models.GraphMeta{MemoryRefs: []string{"original"}, SandboxCapabilities: &models.SandboxCapabilitySnapshot{PythonVersions: []string{"original"}}},
		RuntimePolicy: models.RuntimePolicy{
			HardLimits: models.RuntimeHardLimits{AllowedRegistries: []string{"original"}},
			Grants:     []models.RuntimeGrant{{GrantedBy: "original", ExpiresAt: &now}}, AcknowledgedAt: &now,
		},
		EnvironmentManifest: &models.EnvironmentManifest{DatasetHashes: map[string]string{"source": "original"}, RunCommand: []string{"original"}, FinishedAt: &now},
		Verification:        verification,
		ModelSnapshot:       &models.ModelSnapshot{Text: &models.ProfileSnapshot{Model: "original"}, Vision: []models.ProfileSnapshot{{Model: "original"}}},
		Questions:           []models.PlanQuestion{{Options: []models.QuestionOption{{Label: "original"}}, AnsweredAt: &now, ExpiresAt: &now}},
		AgentSnapshot:       []models.AgentProfileSnapshot{{PromptHash: "original"}},
		Acceptance:          &models.AcceptanceContract{Goals: []models.AcceptanceGoal{{EvidenceRefs: []string{"original"}}}},
		OutcomeDetails:      &models.OutcomeDetails{AcceptedGoals: []models.AcceptedGoalDetail{{EvidenceRefs: []string{"original"}}}, Limitations: []string{"original"}},
		RuntimeActions:      []models.RuntimeActionRequest{{Details: map[string]any{"nested": []string{"original"}}, DecidedAt: &now}},
		ToolCalls:           []models.ToolCallRecord{{Arguments: map[string]any{"nested": []string{"original"}}, FinishedAt: &now}},
		TerminalCommittedAt: &now,
		Nodes: []*models.TaskNode{{
			ID: "node-1", Name: "original", Enabled: &enabled, Optional: &optional,
			Dependencies: []string{"original"}, OptionalDependencies: []string{"original"},
			RequiredArtifacts: []string{"original"}, OutputArtifacts: []string{"original"},
			InputBindings: []models.InputBinding{{Target: "original"}}, RuntimeActionIDs: []string{"original"},
			Contract:     models.TaskContract{InputArtifacts: []string{"original"}, OutputArtifacts: []string{"original"}, AllowedTools: []string{}},
			Inputs:       map[string]any{"metrics": map[string]any{"score": 1}, "rows": []map[string]any{{"name": "original"}}},
			Metadata:     map[string]any{"labels": map[string]string{"value": "original"}, "report": &isolationDiagnostic{Counts: []int{1}}},
			Usage:        &models.TaskUsage{Models: map[string]int{"original": 1}, ModelBreakdown: []models.ModelUsage{{Model: "original"}}},
			Verification: verification, StartedAt: &now, FinishedAt: &now, LeaseExpiresAt: &now, NextAttemptAt: &now,
			Recovery:       &models.RecoveryState{Ledger: []models.ActionLedgerEntry{{Arguments: []string{"original"}}}, Evidence: []models.Artifact{{Payload: json.RawMessage(`[1]`)}}},
			Failure:        &models.FailureInfo{EvidenceRefs: []string{"original"}},
			ReplanProposal: &models.ReplanProposal{SuggestedChanges: []string{"original"}},
			BlockInfo:      &models.BlockInfo{Causes: []models.BlockCause{{Message: "original"}}},
		}},
		Edges:     []*models.TaskEdge{{ID: "edge-1", Type: "original"}},
		Artifacts: map[string]models.Artifact{"proof": {Payload: json.RawMessage(`[1]`), Metadata: map[string]any{"nested": map[string]any{"score": 1}}}},
	}
}

func mutateIsolationPlan(plan *models.PlanGraph) {
	plan.Title = "mutated"
	*plan.ArchivedAt = plan.ArchivedAt.Add(time.Hour)
	*plan.PausedAt = plan.PausedAt.Add(time.Hour)
	*plan.Approval.ApprovedAt = plan.Approval.ApprovedAt.Add(time.Hour)
	*plan.Usage.StartedAt = plan.Usage.StartedAt.Add(time.Hour)
	*plan.Usage.FinishedAt = plan.Usage.FinishedAt.Add(time.Hour)
	*plan.Usage.ApprovalWaitingSince = plan.Usage.ApprovalWaitingSince.Add(time.Hour)
	plan.Usage.BudgetWarnings[0] = "mutated"
	*plan.Review.ReviewedAt = plan.Review.ReviewedAt.Add(time.Hour)
	plan.MissingRequirements[0].Name = "mutated"
	plan.Context.Attachments[0].Name = "mutated"
	plan.Context.Documents[0].DocumentID = "mutated"
	plan.Context.Paper.Authors[0] = "mutated"
	plan.Context.Paper.Warnings[0] = "mutated"
	plan.Context.Entities["nested"].([]string)[0] = "mutated"
	plan.Context.Constraints["nested"].(map[string]string)["value"] = "mutated"
	plan.Context.MemoryRefs[0] = "mutated"
	plan.Meta.MemoryRefs[0] = "mutated"
	plan.Meta.SandboxCapabilities.PythonVersions[0] = "mutated"
	plan.RuntimePolicy.HardLimits.AllowedRegistries[0] = "mutated"
	plan.RuntimePolicy.Grants[0].GrantedBy = "mutated"
	*plan.RuntimePolicy.Grants[0].ExpiresAt = plan.RuntimePolicy.Grants[0].ExpiresAt.Add(time.Hour)
	*plan.RuntimePolicy.AcknowledgedAt = plan.RuntimePolicy.AcknowledgedAt.Add(time.Hour)
	plan.EnvironmentManifest.DatasetHashes["source"] = "mutated"
	plan.EnvironmentManifest.RunCommand[0] = "mutated"
	*plan.EnvironmentManifest.FinishedAt = plan.EnvironmentManifest.FinishedAt.Add(time.Hour)
	plan.Verification.Issues[0] = "mutated"
	plan.Verification.Images[0].Evidence[0] = "mutated"
	plan.ModelSnapshot.Text.Model = "mutated"
	plan.ModelSnapshot.Vision[0].Model = "mutated"
	plan.Questions[0].Options[0].Label = "mutated"
	*plan.Questions[0].AnsweredAt = plan.Questions[0].AnsweredAt.Add(time.Hour)
	*plan.Questions[0].ExpiresAt = plan.Questions[0].ExpiresAt.Add(time.Hour)
	plan.AgentSnapshot[0].PromptHash = "mutated"
	plan.Acceptance.Goals[0].EvidenceRefs[0] = "mutated"
	plan.OutcomeDetails.AcceptedGoals[0].EvidenceRefs[0] = "mutated"
	plan.OutcomeDetails.Limitations[0] = "mutated"
	plan.RuntimeActions[0].Details["nested"].([]string)[0] = "mutated"
	*plan.RuntimeActions[0].DecidedAt = plan.RuntimeActions[0].DecidedAt.Add(time.Hour)
	plan.ToolCalls[0].Arguments["nested"].([]string)[0] = "mutated"
	*plan.ToolCalls[0].FinishedAt = plan.ToolCalls[0].FinishedAt.Add(time.Hour)
	*plan.TerminalCommittedAt = plan.TerminalCommittedAt.Add(time.Hour)
	node := plan.Nodes[0]
	node.Name = "mutated"
	*node.Enabled = false
	*node.Optional = false
	node.Dependencies[0] = "mutated"
	node.OptionalDependencies[0] = "mutated"
	node.RequiredArtifacts[0] = "mutated"
	node.OutputArtifacts[0] = "mutated"
	node.InputBindings[0].Target = "mutated"
	node.RuntimeActionIDs[0] = "mutated"
	node.Contract.InputArtifacts[0] = "mutated"
	node.Contract.OutputArtifacts[0] = "mutated"
	node.Inputs["metrics"].(map[string]any)["score"] = 999
	node.Inputs["rows"].([]map[string]any)[0]["name"] = "mutated"
	node.Metadata["labels"].(map[string]string)["value"] = "mutated"
	node.Metadata["report"].(*isolationDiagnostic).Counts[0] = 999
	node.Usage.Models["original"] = 999
	node.Usage.ModelBreakdown[0].Model = "mutated"
	node.Verification.Issues[0] = "mutated"
	node.Verification.Images[0].Evidence[0] = "mutated"
	*node.StartedAt = node.StartedAt.Add(time.Hour)
	*node.FinishedAt = node.FinishedAt.Add(time.Hour)
	*node.LeaseExpiresAt = node.LeaseExpiresAt.Add(time.Hour)
	*node.NextAttemptAt = node.NextAttemptAt.Add(time.Hour)
	node.Recovery.Ledger[0].Arguments[0] = "mutated"
	node.Recovery.Evidence[0].Payload[1] = '9'
	node.Failure.EvidenceRefs[0] = "mutated"
	node.ReplanProposal.SuggestedChanges[0] = "mutated"
	node.BlockInfo.Causes[0].Message = "mutated"
	plan.Edges[0].Type = "mutated"
	artifact := plan.Artifacts["proof"]
	artifact.Payload[1] = '9'
	artifact.Metadata["nested"].(map[string]any)["score"] = 999
}

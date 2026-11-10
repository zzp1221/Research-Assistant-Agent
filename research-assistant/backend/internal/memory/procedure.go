package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
)

// RecordRepairProcedures creates review candidates only from actual verified
// actions. One independent execution is a case, not a generalized strategy.
func RecordRepairProcedures(ctx context.Context, store Store, plan *models.PlanGraph, task *models.TaskNode, ledger []models.ActionLedgerEntry) error {
	for _, action := range ledger {
		if !action.Verified || action.Kind != "repair" || action.Action == "" || action.BeforeStateHash == "" || action.AfterStateHash == "" || action.BeforeStateHash == action.AfterStateHash || action.EnvironmentHash == "" || action.ProtocolHash == "" || action.ExecutionID == "" || len(action.BeforeEvidence) == 0 || len(action.AfterEvidence) == 0 || len(action.ArtifactRefs) == 0 {
			continue
		}
		payload, _ := json.Marshal(action)
		rec := Record{ID: "mem-repair-" + recovery.Hash([]string{plan.OwnerID, plan.ID, task.ID, action.ID}), Kind: KindProcedure, Scope: projectExperienceScope(plan), Title: "已验证案例的修复候选：" + task.Name, Content: "适用条件和动作来自单个执行案例；复用时仍需权限与重新验证。\n" + string(payload), Tags: []string{"procedure", "repair", "case-verified"}, Source: Source{Type: "action_ledger", Ref: action.ID, CreatedAt: time.Now().UTC()}, Refs: []SourceRef{{PlanID: plan.ID, TaskID: task.ID, SessionID: plan.SessionID}}, Trust: TrustInferred, Status: StatusPending, Importance: 7, Evidence: Evidence{Class: "execution_derived", Confidence: .9}, Attributes: map[string]any{"environment_hash": action.EnvironmentHash, "protocol_hash": action.ProtocolHash, "independent_cases": 1, "generalization": "single_case", "execution_id": action.ExecutionID, "action_id": action.ID}}
		if existing, err := store.Get(ctx, plan.OwnerID, rec.ID); err == nil && existing.ID != "" {
			continue
		}
		for _, ref := range action.ArtifactRefs {
			rec.Refs = append(rec.Refs, SourceRef{ArtifactRef: ref})
		}
		if strings.EqualFold(os.Getenv("MEMORY_PROCEDURE_PROMOTION"), "true") {
			rec.Status = StatusActive
		}
		if err := putIgnoreDuplicate(ctx, store, rec); err != nil {
			return err
		}
	}
	return nil
}

// RecordProcedureUse distinguishes retrieval from adoption and verified reuse.
// Incompatible or unauthorized suggestions never become successful uses.
func RecordProcedureUse(ctx context.Context, store Store, owner, id, stage, executionID string, successful *bool) error {
	_, err := store.Update(ctx, owner, id, Patch{procedureUse: &procedureUsePatch{stage: stage, executionID: executionID, successful: successful}})
	return err
}

type procedureUsePatch struct {
	stage, executionID string
	successful         *bool
}

// applyProcedureUse runs under the store's update lock so concurrent executions
// cannot replace one another's evidence or separate invalidation from its cause.
func applyProcedureUse(rec *Record, patch procedureUsePatch) (bool, error) {
	stage, executionID, successful := patch.stage, patch.executionID, patch.successful
	if stage != "retrieved" && stage != "adopted" && stage != "executed" && stage != "verified" {
		return false, fmt.Errorf("procedure_stage_invalid")
	}
	if rec.Kind != KindProcedure {
		return false, fmt.Errorf("memory_is_not_procedure")
	}
	attrs := rec.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	var uses []map[string]any
	raw, _ := json.Marshal(attrs["uses"])
	_ = json.Unmarshal(raw, &uses)
	for _, use := range uses {
		if use["stage"] == stage && use["execution_id"] == executionID {
			return false, nil
		}
	}
	entry := map[string]any{"stage": stage, "execution_id": executionID, "at": time.Now().UTC(), "result": "unknown"}
	if stage == "verified" && successful != nil {
		entry["result"] = *successful
		if !*successful {
			entry["invalidated"] = true
			rec.Status = StatusPending
		}
	}
	uses = append(uses, entry)
	attrs["uses"] = uses
	rec.Attributes = attrs
	return true, nil
}

// AdoptProcedures requires explicit task input and an actually retrieved,
// compatible case. A model mentioning a memory in prose is insufficient.
func AdoptProcedures(ctx context.Context, store Store, owner string, requested any, retrieved []string, environment, protocol, execution string) []string {
	allowed := map[string]bool{}
	for _, id := range retrieved {
		allowed[id] = true
	}
	var ids []string
	raw, _ := json.Marshal(requested)
	_ = json.Unmarshal(raw, &ids)
	adopted := []string{}
	for _, id := range ids {
		if !allowed[id] {
			continue
		}
		rec, err := store.Get(ctx, owner, id)
		if err != nil || rec.Kind != KindProcedure || rec.Status != StatusActive || rec.Evidence.Hidden {
			continue
		}
		if rec.Attributes["environment_hash"] != environment || rec.Attributes["protocol_hash"] != protocol {
			continue
		}
		if err := RecordProcedureUse(ctx, store, owner, id, "adopted", execution, nil); err == nil {
			adopted = append(adopted, id)
		}
	}
	return adopted
}

func RecordProcedureActions(ctx context.Context, store Store, owner string, ledger []models.ActionLedgerEntry) error {
	for _, action := range ledger {
		if action.Kind != "repair" || len(action.ArtifactRefs) == 0 || len(action.BeforeEvidence) == 0 || len(action.AfterEvidence) == 0 {
			continue
		}
		for _, id := range action.ProcedureIDs {
			caseID := action.ExecutionID + "/" + action.ID
			if err := RecordProcedureUse(ctx, store, owner, id, "executed", caseID, nil); err != nil {
				return err
			}
			passed := action.Verified
			if err := RecordProcedureUse(ctx, store, owner, id, "verified", caseID, &passed); err != nil {
				return err
			}
		}
	}
	return nil
}

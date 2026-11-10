package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
)

// RecordTrial turns an AutoResearch ledger into negative/procedure/fact
// memories. Holdout command fields are never copied.
func RecordTrial(ctx context.Context, store Store, plan *models.PlanGraph, ledger models.ResearchTrialLedger) error {
	if store == nil || plan == nil || strings.TrimSpace(plan.OwnerID) == "" {
		return nil
	}
	now := time.Now().UTC()
	scope := projectExperienceScope(plan)
	for _, trial := range ledger.Trials {
		if trial.Number == 0 && trial.Status == "baseline" {
			continue
		}
		metric := ""
		if trial.Metric != nil {
			metric = fmt.Sprintf("%.8g", *trial.Metric)
		}
		if trial.Status == "rejected" || trial.Status == "failed" || trial.Status == "compromised" {
			rec := Record{
				ID: fmt.Sprintf("mem-trial-%s-%d", plan.ID, trial.Number), Kind: KindNegative, Scope: scope,
				Title:   fmt.Sprintf("AutoResearch trial %d 被拒绝", trial.Number),
				Content: truncateRunes(fmt.Sprintf("假设：%s；决策：%s；原因：%s；指标：%s", trial.Hypothesis, trial.Decision, trial.Reason, metric), 700),
				Tags:    []string{"autoresearch", "negative"}, Entities: []Entity{{Type: "plan", Value: plan.ID}},
				Source:   Source{Type: "trial", Ref: fmt.Sprintf("%s#%d", plan.ID, trial.Number), CreatedAt: now},
				Refs:     []SourceRef{{PlanID: plan.ID, SessionID: plan.SessionID}},
				Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: &trial.FinishedAt, RecordedAt: now},
				Importance: 8, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
			}
			if _, err := store.Put(ctx, rec); err != nil {
				return err
			}
			continue
		}
		if trial.Status == "kept" {
			// SearchRuns counts the whole campaign. Only this trial's own
			// successful evaluator runs can support independent case counts.
			independent := 0
			seen := map[string]bool{}
			for _, run := range trial.EvalResults {
				if run.ExitCode == 0 && run.ExecutionID != "" {
					key := run.ExecutionID
					if !seen[key] {
						seen[key] = true
						independent++
					}
				}
			}
			if independent >= 2 && len(trial.Patches) > 0 && ledger.SpecSHA256 != "" && plan.EnvironmentManifest != nil {
				rec := Record{
					ID: fmt.Sprintf("mem-trial-proc-%s-%d", plan.ID, trial.Number), Kind: KindProcedure, Scope: scope,
					Title:   fmt.Sprintf("AutoResearch 有效策略 trial %d", trial.Number),
					Content: truncateRunes(fmt.Sprintf("指标 %s；假设：%s；决策：%s；原因：%s", metric, trial.Hypothesis, trial.Decision, trial.Reason), 700),
					Tags:    []string{"autoresearch", "procedure"}, Entities: []Entity{{Type: "metric", Value: ledger.MetricKey}},
					Source:   Source{Type: "trial", Ref: fmt.Sprintf("%s#%d", plan.ID, trial.Number), CreatedAt: now},
					Refs:     []SourceRef{{PlanID: plan.ID, SessionID: plan.SessionID}},
					Evidence: Evidence{Class: "execution_derived", Confidence: 0.85}, Validity: Validity{ValidFrom: &trial.FinishedAt, RecordedAt: now},
					Importance: 7, Confidence: 0.85, Trust: TrustInferred, Sensitivity: "private", Status: StatusPending,
					Attributes: map[string]any{"independent_cases": independent, "protocol_hash": ledger.SpecSHA256, "environment_hash": recovery.Hash(plan.EnvironmentManifest)},
				}
				if strings.EqualFold(os.Getenv("MEMORY_PROCEDURE_PROMOTION"), "true") {
					rec.Status = StatusActive
				}
				if _, err := store.Put(ctx, rec); err != nil {
					return err
				}
			} else {
				rec := Record{ID: fmt.Sprintf("mem-trial-case-%s-%d", plan.ID, trial.Number), Kind: KindEpisode, Scope: scope, Title: fmt.Sprintf("AutoResearch trial %d 执行案例", trial.Number), Content: truncateRunes(fmt.Sprintf("假设：%s；指标：%s；独立验证：%d；单次案例不自动晋升通用方法", trial.Hypothesis, metric, independent), 700), Tags: []string{"autoresearch", "case"}, Source: Source{Type: "trial", Ref: fmt.Sprintf("%s#%d", plan.ID, trial.Number), CreatedAt: now}, Trust: TrustVerified, Status: StatusActive, Attributes: map[string]any{"independent_cases": independent}}
				if _, err := store.Put(ctx, rec); err != nil {
					return err
				}
			}
		}
	}
	fact := Record{
		ID: "mem-metric-" + plan.ID, Kind: KindFact, Scope: scope,
		Title:   "AutoResearch 指标：" + ledger.MetricKey,
		Content: fmt.Sprintf("metric=%s direction=%s baseline=%.8g best=%.8g accepted=%d/%d stop=%s", ledger.MetricKey, ledger.Direction, ledger.BaselineScore, ledger.BestScore, ledger.AcceptedTrials, ledger.CompletedTrials, ledger.StopReason),
		Tags:    []string{"autoresearch", "metric"}, Entities: []Entity{{Type: "metric", Value: ledger.MetricKey}},
		Source:   Source{Type: "trial", Ref: plan.ID, CreatedAt: now},
		Refs:     []SourceRef{{PlanID: plan.ID, SessionID: plan.SessionID}},
		Evidence: Evidence{Class: "execution_derived", Confidence: 0.95}, Validity: Validity{ValidFrom: &ledger.FinishedAt, RecordedAt: now},
		Importance: 8, Confidence: 0.95, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
	}
	_, err := store.Put(ctx, fact)
	return err
}

package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// RecordTrial turns an AutoResearch ledger into negative/procedure/fact
// memories. Holdout command fields are never copied.
func RecordTrial(ctx context.Context, store Store, plan *models.PlanGraph, ledger models.ResearchTrialLedger) error {
	if store == nil || plan == nil || strings.TrimSpace(plan.OwnerID) == "" {
		return nil
	}
	now := time.Now().UTC()
	scope := Scope{OwnerID: plan.OwnerID, ProjectID: plan.ProjectID, SessionID: plan.SessionID, PlanID: plan.ID, AgentID: "research_coding_agent"}
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
				Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: &trial.FinishedAt, RecordedAt: now},
				Importance: 8, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
			}
			if _, err := store.Put(ctx, rec); err != nil {
				return err
			}
			continue
		}
		if trial.Status == "kept" {
			if ledger.SearchRuns >= 2 {
				rec := Record{
					ID: fmt.Sprintf("mem-trial-proc-%s-%d", plan.ID, trial.Number), Kind: KindProcedure, Scope: scope,
					Title:   fmt.Sprintf("AutoResearch 有效策略 trial %d", trial.Number),
					Content: truncateRunes(fmt.Sprintf("指标 %s；假设：%s；决策：%s；原因：%s", metric, trial.Hypothesis, trial.Decision, trial.Reason), 700),
					Tags:    []string{"autoresearch", "procedure"}, Entities: []Entity{{Type: "metric", Value: ledger.MetricKey}},
					Source:   Source{Type: "trial", Ref: fmt.Sprintf("%s#%d", plan.ID, trial.Number), CreatedAt: now},
					Evidence: Evidence{Class: "execution_derived", Confidence: 0.85}, Validity: Validity{ValidFrom: &trial.FinishedAt, RecordedAt: now},
					Importance: 7, Confidence: 0.85, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
				}
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
		Evidence: Evidence{Class: "execution_derived", Confidence: 0.95}, Validity: Validity{ValidFrom: &ledger.FinishedAt, RecordedAt: now},
		Importance: 8, Confidence: 0.95, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
	}
	_, err := store.Put(ctx, fact)
	return err
}

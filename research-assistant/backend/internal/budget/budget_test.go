package budget

import (
	"errors"
	"testing"
	"time"
)

func TestManagerReserveSettleAndSummary(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	if _, err := manager.SetBudget("owner-a", "day", 1.0, "soft"); err != nil {
		t.Fatal(err)
	}
	reservation, err := manager.Reserve("owner-a", "plan-1", "task-1", 0.4, 1000)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if summary := manager.Summary("owner-a", "day"); summary.ReservedUSD != 0.4 || summary.RemainingUSD != 0.6 {
		t.Fatalf("reserved summary wrong: %#v", summary)
	}
	if _, err := manager.Reserve("owner-a", "plan-1", "task-2", 0.8, 1000); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget exceeded, got %v", err)
	}
	manager.Settle(reservation, Entry{OwnerID: "owner-a", PlanID: "plan-1", TaskID: "task-1", Model: "m1", Prompt: 100, Completion: 50, CostUSD: 0.35})
	summary := manager.Summary("owner-a", "day")
	if summary.SpentUSD != 0.35 || summary.ReservedUSD != 0 || summary.RemainingUSD != 0.65 {
		t.Fatalf("summary after settle: %#v", summary)
	}
	if len(summary.TopPlans) != 1 || summary.TopPlans[0].PlanID != "plan-1" {
		t.Fatalf("top plans: %#v", summary.TopPlans)
	}
}

func TestManagerNoBudgetNeverBlocks(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	if _, err := manager.Reserve("owner-b", "plan-1", "task-1", 999, 999); err != nil {
		t.Fatalf("unconfigured budget must not block: %v", err)
	}
	summary := manager.Summary("owner-b", "day")
	if summary.Configured {
		t.Fatalf("summary should be unconfigured")
	}
}

func TestManagerReleasePlanAndAlerts(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	if _, err := manager.SetBudget("owner-a", "month", 10, "hard"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reserve("owner-a", "plan-x", "t1", 1, 10); err != nil {
		t.Fatal(err)
	}
	manager.ReleasePlan("plan-x")
	if summary := manager.Summary("owner-a", "month"); summary.ReservedUSD != 0 {
		t.Fatalf("release plan left reservation: %#v", summary)
	}
	manager.RecordAlert(Alert{OwnerID: "owner-a", PlanID: "plan-x", Kind: "warning", Policy: "soft", Message: "80%"})
	alerts := manager.Alerts("owner-a", 10)
	if len(alerts) != 1 || alerts[0].Kind != "warning" {
		t.Fatalf("alerts: %#v", alerts)
	}
}

func TestManagerTimeseriesGroups(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	manager.Record(Entry{OwnerID: "owner-a", PlanID: "p1", Model: "m1", Prompt: 10, Completion: 5, CostUSD: 0.1, At: now.Add(-time.Hour)})
	manager.Record(Entry{OwnerID: "owner-a", PlanID: "p2", Model: "m2", Prompt: 20, Completion: 5, CostUSD: 0.2, At: now.Add(-2 * time.Hour)})
	byModel := manager.Timeseries("owner-a", now.Add(-24*time.Hour), now, "model")
	if len(byModel) != 2 {
		t.Fatalf("by model points: %#v", byModel)
	}
	byPlan := manager.Timeseries("owner-a", now.Add(-24*time.Hour), now, "plan")
	if len(byPlan) != 2 {
		t.Fatalf("by plan points: %#v", byPlan)
	}
	byDay := manager.Timeseries("owner-a", now.Add(-24*time.Hour), now, "day")
	if len(byDay) != 1 || byDay[0].CostUSD < 0.299 || byDay[0].Calls != 2 {
		t.Fatalf("by day points: %#v", byDay)
	}
}

func TestSetBudgetValidation(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	if _, err := manager.SetBudget("owner-a", "week", 1, "soft"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid period, got %v", err)
	}
	if _, err := manager.SetBudget("owner-a", "day", -1, "soft"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid limit, got %v", err)
	}
}

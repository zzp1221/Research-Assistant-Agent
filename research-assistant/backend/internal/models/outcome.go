package models

// PlanOutcome is the semantic outcome of a plan. It is deliberately separate
// from TaskStatus: TaskStatus drives scheduler dependency logic, while
// PlanOutcome tells users whether the produced result is actually usable.
type PlanOutcome string

const (
	OutcomeCompleted   PlanOutcome = "completed"
	OutcomePartial     PlanOutcome = "partial"
	OutcomeSmoke       PlanOutcome = "smoke"
	OutcomeMock        PlanOutcome = "mock"
	OutcomeBlocked     PlanOutcome = "blocked"
	OutcomeFailed      PlanOutcome = "failed"
	OutcomeNeedsReview PlanOutcome = "needs_review"
)

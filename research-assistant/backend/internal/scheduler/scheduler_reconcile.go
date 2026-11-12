package scheduler

import (
	"context"
	"log"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

// finishExternalTerminal publishes the terminal event and runs the best-effort
// side effects for a stop decided outside the execution loop (an approval
// rejection, an approval timeout or a question timeout). The terminal marker
// keeps it from repeating.
func (s *Scheduler) finishExternalTerminal(ctx context.Context, planID string) {
	s.completeTerminalOnce(ctx, planID)
}

// completeTerminalOnce claims the terminal transition and runs its side effects.
// It is the single entry point used by reconciliation, external timeouts and the
// startup compensation scan, so every terminal path shares one one-shot rule.
func (s *Scheduler) completeTerminalOnce(ctx context.Context, planID string) {
	if !s.claimTerminalCommit(planID) {
		return
	}
	s.finalizeTerminalCommit(ctx, planID)
}

// ReconcileTerminalCommits completes plans that reached a terminal status but
// whose terminal pipeline never ran, for example because the process stopped
// between the status commit and the side effects. It is safe to call on every
// startup and on every plan read: already-committed plans are left untouched.
func (s *Scheduler) ReconcileTerminalCommits(ctx context.Context, planID string) bool {
	if s == nil || s.store == nil {
		return false
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil || plan == nil || plan.TerminalCommittedAt != nil {
		return false
	}
	switch plan.Status {
	case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
	default:
		return false
	}
	if plan.Outcome == "" {
		// A terminal plan without an outcome cannot explain itself; rebuild it
		// through the normal reconciliation instead of publishing a placeholder.
		_, _ = s.reconcilePlanState(ctx, plan)
		return true
	}
	s.completeTerminalOnce(ctx, planID)
	return true
}

// reconcileOutcome is the decision of one terminal-state reconciliation.
type reconcileOutcome int

const (
	// reconcileWait means the plan still has legitimate work (running, retry,
	// pause, approval, question) and the loop should wait.
	reconcileWait reconcileOutcome = iota
	// reconcileContinue means new runnable work appeared; the loop must dispatch.
	reconcileContinue
	// reconcileTerminal means the plan reached a committed terminal status.
	reconcileTerminal
)

// transition returns true when the reconciliation committed any state change.
func (o reconcileOutcome) transition() bool {
	return o != reconcileWait
}

// reconcilePlanState is the single terminal-state decision point. Its argument
// is only a hint about when to try: every branch re-reads the committed graph
// inside UpdatePlan and decides on that state, so a stale snapshot can never
// write a terminal status without an outcome.
func (s *Scheduler) reconcilePlanState(ctx context.Context, hint *models.PlanGraph) (reconcileOutcome, error) {
	outcome := reconcileWait
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(hint.ID, func(plan *models.PlanGraph) error {
		outcome = reconcileWait
		events = events[:0]
		switch plan.Status {
		case models.StatusCanceled, models.StatusCompleted, models.StatusFailed:
			// An explicit stop keeps its own reason and outcome. Nothing may
			// reinterpret it, least of all turn blocked dependents into failed.
			outcome = reconcileTerminal
			return nil
		}
		if plan.Paused {
			outcome = reconcileWait
			return nil
		}
		// Promote anything the committed state allows.
		promoted := s.promotePendingNodesToReady(plan)
		events = append(events, promoted...)
		if len(promoted) > 0 {
			fillMeta(plan)
			outcome = reconcileContinue
			return nil
		}
		if plan.Status == models.StatusAwaitingInput && !allTerminal(plan) {
			outcome = reconcileWait
			return nil
		}
		// A runnable node that could not be dispatched (for example because the
		// global concurrency slots are saturated) is waiting for resources, not
		// deadlocked: return to the loop instead of writing a terminal status.
		if hasRunnableNode(plan) {
			outcome = reconcileContinue
			return nil
		}
		// A legal wait, a retry deadline or a running task means the plan is not
		// deadlocked: waiting is the correct decision.
		if deferrableWorkPending(plan) {
			outcome = reconcileWait
			return nil
		}
		if !allTerminal(plan) {
			// Work remains that can never run: only now is it a dependency
			// deadlock. Persist the unsatisfied nodes and reasons first.
			events = append(events, markUnsatisfiedNodes(plan, time.Now())...)
			evaluation := s.evaluatePlanTerminal(ctx, plan, terminalStalled)
			applyPlanEvaluation(plan, evaluation)
			fillMeta(plan)
			outcome = reconcileTerminal
			return nil
		}
		// Every node ended: summarize the run through the acceptance rules.
		evaluation := s.evaluatePlanTerminal(ctx, plan, terminalCompleted)
		applyPlanEvaluation(plan, evaluation)
		fillMeta(plan)
		outcome = reconcileTerminal
		return nil
	})
	if err != nil {
		return reconcileWait, err
	}
	for _, event := range events {
		s.publishAndStore(hint.ID, event)
	}
	// Terminal side effects run exactly once, on the transition that committed
	// the terminal result. A later wake-up that observes an already-terminal plan
	// must not replay the completion event, the cleanup or the notification — but
	// an already-terminal plan whose pipeline never ran (the process stopped
	// between the commit and the side effects) is completed here, which is also
	// what repairs a plan that crashed mid-finalization.
	if outcome == reconcileTerminal {
		s.completeTerminalOnce(ctx, hint.ID)
	}
	return outcome, nil
}

// hasRunnableNode reports whether an enabled node is ready to run right now.
func hasRunnableNode(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	now := time.Now()
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusReady || !node.IsEnabled() {
			continue
		}
		if node.NextAttemptAt != nil && now.Before(*node.NextAttemptAt) {
			continue
		}
		return true
	}
	return false
}

// deferrableWorkPending reports whether the plan still has legitimate work that
// the scheduler should wait for instead of declaring a deadlock.
func deferrableWorkPending(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	now := time.Now()
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusInProgress, models.StatusAwaitingApproval, models.StatusAwaitingInput:
			return true
		case models.StatusPending, models.StatusReady:
			if node.NextAttemptAt != nil && now.Before(*node.NextAttemptAt) {
				return true
			}
		}
	}
	if hasPendingQuestions(plan) || hasPendingRuntimeAction(plan) {
		return true
	}
	return false
}

func hasPendingQuestions(plan *models.PlanGraph) bool {
	for _, question := range plan.Questions {
		if question.Status == models.QuestionPending {
			return true
		}
	}
	return false
}

func hasPendingRuntimeAction(plan *models.PlanGraph) bool {
	for _, action := range plan.RuntimeActions {
		if action.Status == models.RuntimeActionPending {
			return true
		}
	}
	return false
}

// markUnsatisfiedNodes persists the machine-readable reason for every node that
// can never run: the real unsatisfied dependencies and the missing artifacts.
func markUnsatisfiedNodes(plan *models.PlanGraph, now time.Time) []models.PlanEvent {
	events := []models.PlanEvent{}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if node.Status != models.StatusPending && node.Status != models.StatusReady {
			continue
		}
		if !node.IsEnabled() {
			continue
		}
		missingArtifacts := []string{}
		for _, key := range node.RequiredArtifacts {
			if _, ok := plan.Artifacts[key]; !ok {
				missingArtifacts = append(missingArtifacts, key)
			}
		}
		unsatisfied := []string{}
		for _, depID := range hardDependencyIDs(plan, node) {
			dep := getNodeByID(plan, depID)
			if dep == nil || (dep.Status != models.StatusCompleted && dep.Status != models.StatusSkipped) {
				unsatisfied = append(unsatisfied, depID)
			}
		}
		if len(unsatisfied) == 0 && len(missingArtifacts) == 0 {
			continue
		}
		code := models.BlockUnsatisfiedDependencies
		message := "硬依赖未满足：" + strings.Join(unsatisfied, ", ")
		if len(unsatisfied) == 0 {
			code = models.BlockMissingArtifacts
			message = "必需产物缺失：" + strings.Join(missingArtifacts, ", ")
		}
		node.BlockInfo = &models.BlockInfo{
			Code:       code,
			Message:    message,
			OccurredAt: now,
			Causes: []models.BlockCause{{
				Code:       code,
				RootTaskID: node.ID,
				Status:     node.Status,
				Message:    message,
			}},
		}
		node.Status = models.StatusBlocked
		node.Error = message
		node.FinishedAt = &now
		node.UpdatedAt = now
		if len(missingArtifacts) > 0 {
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, models.MissingRequirement{
				Kind:        "artifact",
				Name:        strings.Join(missingArtifacts, ","),
				Detail:      message,
				Remediation: "补齐上游产物或修正依赖后重跑该任务。",
			})
		}
		events = append(events, models.PlanEvent{
			PlanID:     plan.ID,
			EventType:  "task_blocked",
			TaskID:     node.ID,
			TaskStatus: string(models.StatusBlocked),
			Payload: map[string]any{
				"reason":                 code,
				"block_info":             node.BlockInfo,
				"unsatisfied_dependency": unsatisfied,
				"missing_artifacts":      missingArtifacts,
			},
			Timestamp: now,
		})
	}
	if len(events) > 0 {
		plan.UpdatedAt = now
	}
	return events
}

// terminalReason tells the evaluator which state it is deciding on.
type terminalReason int

const (
	terminalCompleted terminalReason = iota
	terminalStalled
)

// evaluatePlanTerminal computes the terminal evaluation (status, outcome,
// reason, details) for the committed graph. It never mutates the graph and
// never performs I/O, so it can run inside the state transition.
func (s *Scheduler) evaluatePlanTerminal(ctx context.Context, plan *models.PlanGraph, reason terminalReason) models.PlanFailureEvaluation {
	now := time.Now()
	evaluation := strictPlanEvaluation(plan, reason == terminalStalled, now)

	// The finalizer refines the outcome with the semantic evidence rules. It runs
	// on every graph that ended, not only the ones without failures: otherwise a
	// fully blocked plan would be reported as completed.
	if s.finalizer != nil && reason != terminalStalled {
		if err := s.finalizer.Finalize(ctx, plan); err != nil {
			evaluation.Status = models.StatusFailed
			evaluation.Outcome = models.OutcomeFailed
			evaluation.OutcomeReason = err.Error()
			evaluation.Details = s.buildOutcomeDetails(plan, evaluation)
			evaluation.EventType = "plan_failed"
			return evaluation
		}
		switch plan.Outcome {
		case "":
			// The finalizer declined to decide; the scheduler verdict stands.
		default:
			evaluation.Outcome = plan.Outcome
			if plan.OutcomeReason != "" {
				evaluation.OutcomeReason = plan.OutcomeReason
			}
			evaluation.Status = statusForOutcome(plan.Outcome)
		}
	}
	if evaluation.Outcome == "" {
		evaluation.Outcome = models.OutcomeFailed
	}
	if evaluation.OutcomeReason == "" {
		evaluation.OutcomeReason = defaultOutcomeReason(evaluation.Outcome)
	}
	evaluation.Details = s.buildOutcomeDetails(plan, evaluation)
	evaluation.EventType = "plan_completed"
	if evaluation.Status == models.StatusFailed {
		evaluation.EventType = "plan_failed"
	}
	return evaluation
}

// strictPlanEvaluation is the scheduler's own verdict, used when no finalizer is
// injected. It follows the same core/optional rules: a real core failure fails
// the plan, a missing core condition blocks it, and optional losses alone never
// change a stopped plan into a success.
func strictPlanEvaluation(plan *models.PlanGraph, stalled bool, now time.Time) models.PlanFailureEvaluation {
	evaluation := models.PlanFailureEvaluation{
		Status:     models.StatusCompleted,
		Outcome:    models.OutcomeCompleted,
		FinishedAt: now,
	}
	evidence := planner.EvaluateAcceptanceEvidence(plan)
	switch {
	case stalled:
		evaluation.Status = models.StatusFailed
		evaluation.Outcome = models.OutcomeFailed
		evaluation.OutcomeReason = "unsatisfied dependencies: " + unsatisfiedSummary(plan)
	case evidence.CoreFailed():
		evaluation.Status = models.StatusFailed
		evaluation.Outcome = models.OutcomeFailed
		evaluation.OutcomeReason = "required tasks failed: " + strings.Join(limitStrings(evidence.CoreFailures, 5), ",")
	case evidence.CoreStopped():
		// A block, a cancellation, an unfinished core task or a required task that
		// is missing all mean the core goal was never achieved. It is a missing
		// condition, not a completed plan.
		evaluation.Status = models.StatusFailed
		evaluation.Outcome = models.OutcomeBlocked
		stopped := append(append(append([]string{}, evidence.CoreBlocked...), evidence.CoreCanceled...), evidence.MissingContracts...)
		evaluation.OutcomeReason = "required conditions are missing: " + strings.Join(limitStrings(stopped, 5), ",")
	case evidence.ContractReason != "":
		// A contradictory contract finished executing but proves nothing about the
		// core goal. It is a review case, not a failure.
		evaluation.Status = models.StatusCompleted
		evaluation.Outcome = models.OutcomeNeedsReview
		evaluation.OutcomeReason = evidence.ContractReason
	case evidence.HasContract && !evidence.GoalsVerified():
		evaluation.Status = models.StatusCompleted
		evaluation.Outcome = models.OutcomeNeedsReview
		evaluation.OutcomeReason = "acceptance goals are not verified"
	}
	return evaluation
}

// statusForOutcome maps a semantic outcome onto the execution status it implies.
//
// Status answers "did the run finish?", Outcome answers "is the result usable?".
// A run whose tasks all completed but whose evidence needs review finished
// successfully: it must stay completed/needs_review instead of being reported as
// an execution failure. Only outcomes that represent a real stop fail the run.
func statusForOutcome(outcome models.PlanOutcome) models.TaskStatus {
	switch outcome {
	case models.OutcomeCompleted, models.OutcomePartial, models.OutcomeSmoke, models.OutcomeMock, models.OutcomeNeedsReview:
		return models.StatusCompleted
	default:
		return models.StatusFailed
	}
}

func defaultOutcomeReason(outcome models.PlanOutcome) string {
	switch outcome {
	case models.OutcomeCompleted:
		return "all required tasks completed"
	case models.OutcomePartial:
		return "core goal accepted with optional losses"
	case models.OutcomeBlocked:
		return "required conditions are missing"
	case models.OutcomeNeedsReview:
		return "execution finished but the evidence needs review"
	case models.OutcomeSmoke:
		return "plan ran in smoke mode"
	case models.OutcomeMock:
		return "plan ran against mock data"
	default:
		return string(outcome)
	}
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

// applyPlanEvaluation commits one evaluated terminal result in a single state
// transition: Status, Outcome, OutcomeReason, OutcomeDetails, FinishedAt.
func applyPlanEvaluation(plan *models.PlanGraph, evaluation models.PlanFailureEvaluation) {
	if plan == nil {
		return
	}
	finishedAt := evaluation.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	plan.Status = evaluation.Status
	plan.Outcome = evaluation.Outcome
	plan.OutcomeReason = evaluation.OutcomeReason
	plan.OutcomeDetails = evaluation.Details
	plan.Usage.FinishedAt = &finishedAt
	plan.UpdatedAt = finishedAt
}

// finalizeTerminalCommit publishes the committed outcome and runs the best
// effort side effects exactly once per terminal transition. The caller must have
// claimed the transition through claimTerminalCommit first.
func (s *Scheduler) finalizeTerminalCommit(ctx context.Context, planID string) {
	latest, err := s.store.GetPlan(planID)
	if err != nil || latest == nil {
		logPlanFinalize(planID, err)
		return
	}
	if latest.Outcome == "" {
		// Defensive: a terminal plan must always explain itself.
		plan := latest
		evaluation := models.PlanFailureEvaluation{
			Status:        latest.Status,
			Outcome:       models.OutcomeFailed,
			OutcomeReason: "terminal result was not recorded",
			FinishedAt:    time.Now(),
			EventType:     "plan_failed",
		}
		_ = s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
			applyPlanEvaluation(current, evaluation)
			fillMeta(current)
			return nil
		})
		latest = plan
	}
	eventType := "plan_completed"
	if latest.Status == models.StatusFailed {
		eventType = "plan_failed"
	}
	if latest.Status == models.StatusCanceled {
		eventType = "plan_canceled"
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: eventType,
		Payload: map[string]any{
			"outcome":         string(latest.Outcome),
			"outcome_reason":  latest.OutcomeReason,
			"outcome_details": latest.OutcomeDetails,
		},
		Timestamp: time.Now(),
	})
	if s.memorySink != nil {
		_ = s.memorySink.RecordPlan(ctx, latest)
	}
	s.cleanupPlanSandboxes(context.Background(), planID)
	s.releasePlanBudget(planID)
	s.recordTerminalMetrics(planID)
	if s.onTerminal != nil {
		s.onTerminal(ctx, latest)
	}
}

func unsatisfiedSummary(plan *models.PlanGraph) string {
	ids := []string{}
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusBlocked {
			continue
		}
		if summary := blockInfoSummary(node); summary != "" {
			ids = append(ids, node.ID+"("+summary+")")
			continue
		}
		ids = append(ids, node.ID)
	}
	if len(ids) > 5 {
		ids = ids[:5]
	}
	return strings.Join(ids, ",")
}

func logPlanFinalize(planID string, err error) {
	if err != nil {
		log.Printf("scheduler: cannot finalize plan %s: %v", planID, err)
	}
}

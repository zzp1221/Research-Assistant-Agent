package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/revision"

	"github.com/gin-gonic/gin"
)

// revisionSummary is the list representation without the (potentially large)
// graph snapshot.
type revisionSummary struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	Revision       int        `json:"revision"`
	ParentRevision int        `json:"parent_revision"`
	Status         string     `json:"status"`
	CreatedBy      string     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	Reason         string     `json:"reason,omitempty"`
	DiffSummary    []string   `json:"diff_summary,omitempty"`
	Hash           string     `json:"hash,omitempty"`
	ApprovedBy     string     `json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
}

func summarizeRevision(rev *models.PlanRevision) revisionSummary {
	if rev == nil {
		return revisionSummary{}
	}
	return revisionSummary{
		ID: rev.ID, PlanID: rev.PlanID, Revision: rev.Revision, ParentRevision: rev.ParentRevision,
		Status: rev.Status, CreatedBy: rev.CreatedBy, CreatedAt: rev.CreatedAt, Reason: rev.Reason,
		DiffSummary: rev.DiffSummary, Hash: rev.Hash, ApprovedBy: rev.ApprovedBy, ApprovedAt: rev.ApprovedAt,
	}
}

func (r *planRuntime) revisionStore() revision.Store {
	if r.revisions == nil {
		// The runtime is always constructed with a store, but a nil check keeps
		// old unit tests that build it directly compilable.
		r.revisions = revision.NewMemoryStore()
	}
	return r.revisions
}

// ensureInitialRevision lazily materialises revision 1 from the current plan
// graph for plans created before revisions existed. It is idempotent.
func (r *planRuntime) ensureInitialRevision(plan *models.PlanGraph) (*models.PlanRevision, error) {
	store := r.revisionStore()
	latest, err := store.Latest(plan.ID)
	if err == nil {
		return latest, nil
	}
	if !errors.Is(err, revision.ErrNotFound) {
		return nil, err
	}
	created, err := store.Create(&models.PlanRevision{
		PlanID:    plan.ID,
		Graph:     plan,
		Status:    models.RevisionDraft,
		CreatedBy: plan.OwnerID,
		Reason:    "initial revision (lazy migration)",
	}, 0)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func parseRevisionParam(c *gin.Context) (int, bool) {
	value, err := strconv.Atoi(c.Param("revision"))
	if err != nil || value <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "revision must be a positive integer", "code": "invalid_revision"})
		return 0, false
	}
	return value, true
}

func (r *planRuntime) listRevisions(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if _, err := r.ensureInitialRevision(plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	includeGraph := strings.EqualFold(c.Query("include_graph"), "true")
	list, err := r.revisionStore().List(plan.ID, includeGraph)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	for i := range list {
		list[i].Status = effectiveRevisionStatus(plan, &list[i])
	}
	items := make([]revisionSummary, 0, len(list))
	for i := range list {
		items = append(items, summarizeRevision(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{
		"revisions":        items,
		"current_revision": plan.CurrentRevision,
		"latest_revision":  latestRevisionNumber(list),
		"plan_id":          plan.ID,
	})
}

func latestRevisionNumber(list []models.PlanRevision) int {
	if len(list) == 0 {
		return 0
	}
	return list[len(list)-1].Revision
}

func (r *planRuntime) getRevision(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	number, ok := parseRevisionParam(c)
	if !ok {
		return
	}
	if _, err := r.ensureInitialRevision(plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	rev, err := r.revisionStore().Get(plan.ID, number)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "revision not found", "code": "revision_not_found"})
		return
	}
	detail := *rev
	detail.Status = effectiveRevisionStatus(plan, rev)
	c.JSON(http.StatusOK, gin.H{"revision": &detail})
}

type createRevisionPayload struct {
	BaseRevision int               `json:"base_revision"`
	Reason       string            `json:"reason,omitempty"`
	Graph        *models.PlanGraph `json:"graph"`
}

// createRevision stores an immutable snapshot of an edited graph. The base
// revision must match the latest revision, which provides optimistic locking:
// two clients editing the same revision cannot silently overwrite each other.
func (r *planRuntime) createRevision(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload createRevisionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid revision payload", "code": "invalid_revision"})
		return
	}
	if payload.Graph == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "graph is required", "code": "invalid_revision"})
		return
	}
	// Editing a running plan is never allowed; it would make the executing
	// revision ambiguous. Awaiting-review plans may be edited (draft) but not
	// executed until approved again.
	if plan.Status == models.StatusInProgress || plan.Status == models.StatusAwaitingApproval || plan.Status == models.StatusAwaitingInput {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running or waiting; cancel or finish before editing", "code": "plan_running"})
		return
	}
	if _, err := r.ensureInitialRevision(plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	// Validate the edited graph before storing it, so a broken draft never
	// enters the revision history as valid-looking work.
	validation := validatePlanGraph(payload.Graph)
	if !validation.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan validation failed", "code": "validation_failed", "validation_errors": validation.Errors})
		return
	}
	// The edited graph must keep the plan identity and ownership; everything
	// else is the user's to change.
	payload.Graph.ID = plan.ID
	payload.Graph.OwnerID = plan.OwnerID
	payload.Graph.SessionID = plan.SessionID
	payload.Graph.TraceID = plan.TraceID
	payload.Graph.CreatedAt = plan.CreatedAt
	if payload.Graph.Artifacts == nil {
		payload.Graph.Artifacts = plan.Artifacts
	}
	// The review and runtime-approval gates are server-owned. A client edit may
	// change the graph but must never clear a required review or silently
	// inherit an earlier approval: the new draft needs its own review decision.
	payload.Graph.Approval = plan.Approval
	if plan.Review.Required || plan.Status == models.StatusAwaitingReview {
		payload.Graph.Review = models.ReviewState{
			Required:     true,
			Status:       models.ReviewPending,
			Reason:       plan.Review.Reason,
			Risk:         plan.Review.Risk,
			EstimatedUSD: plan.Review.EstimatedUSD,
		}
	} else {
		payload.Graph.Review = plan.Review
	}
	base := payload.BaseRevision
	if base <= 0 {
		latest, err := r.revisionStore().Latest(plan.ID)
		if err == nil {
			base = latest.Revision
		}
	}
	previous, _ := r.revisionStore().Latest(plan.ID)
	rev, err := r.revisionStore().Create(&models.PlanRevision{
		PlanID:    plan.ID,
		Graph:     payload.Graph,
		Status:    models.RevisionDraft,
		CreatedBy: resolveUserID(c),
		Reason:    strings.TrimSpace(payload.Reason),
	}, base)
	if err != nil {
		var conflict *revision.ConflictError
		if errors.As(err, &conflict) {
			c.JSON(http.StatusConflict, gin.H{
				"error":            "base revision is stale; reload and retry",
				"code":             "revision_conflict",
				"current_revision": conflict.CurrentRevision,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	if previous != nil {
		diff := diffPlanGraphs(previous.Graph, payload.Graph)
		rev.DiffSummary = diffSummary(diff)
	}
	// Apply the draft graph to the editable plan view, keeping runtime state
	// (status/usage/artifacts) that the draft must not clobber.
	now := time.Now().UTC()
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		applyDraftGraph(current, payload.Graph)
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "plan_update_failed"})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_revision_created",
		Revision:  rev.Revision,
		Payload: map[string]any{
			"revision":        rev.Revision,
			"parent_revision": rev.ParentRevision,
			"actor":           resolveUserID(c),
			"reason":          rev.Reason,
			"diff_summary":    rev.DiffSummary,
		},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)
	recordAuditEvent("plan_revision_created", resolveUserID(c), plan.ID, map[string]any{"revision": rev.Revision, "parent_revision": rev.ParentRevision})
	updated, _ := r.store.GetPlan(plan.ID)
	draft := summarizePlan(updated)
	c.JSON(http.StatusOK, gin.H{"revision": summarizeRevision(rev), "plan": draft})
}

// applyDraftGraph copies the editable graph fields into the live plan while
// preserving execution state and history that the draft does not own.
func applyDraftGraph(current, draft *models.PlanGraph) {
	if current == nil || draft == nil {
		return
	}
	current.Nodes = draft.Nodes
	current.Edges = draft.Edges
	current.Budget = draft.Budget
	current.Review = draft.Review
	current.Title = draft.Title
	current.UserIntent = draft.UserIntent
	current.IntentType = draft.IntentType
	if draft.RuntimePolicy.Mode != "" {
		current.RuntimePolicy = draft.RuntimePolicy
	}
	if draft.MissingRequirements != nil {
		current.MissingRequirements = draft.MissingRequirements
	}
	// Drafts may carry artifacts from an edited upstream run, but an empty map
	// must not wipe artifacts the live plan already produced. Only replace when
	// the draft actually contributes artifacts.
	if len(draft.Artifacts) > 0 {
		current.Artifacts = draft.Artifacts
	}
	// A draft that still needs review must visibly hold the plan in the review
	// queue; otherwise the plan library would show "pending" while execution is
	// in fact gated by the review state.
	if current.Review.Required && current.Review.Status == models.ReviewPending {
		switch current.Status {
		case models.StatusInProgress, models.StatusAwaitingApproval, models.StatusAwaitingInput:
		default:
			current.Status = models.StatusAwaitingReview
		}
	}
}

func (r *planRuntime) validateRevision(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	number, ok := parseRevisionParam(c)
	if !ok {
		return
	}
	if _, err := r.ensureInitialRevision(plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	rev, err := r.revisionStore().Get(plan.ID, number)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "revision not found", "code": "revision_not_found"})
		return
	}
	result := validatePlanGraph(rev.Graph)
	c.JSON(http.StatusOK, gin.H{
		"revision": number,
		"valid":    result.Valid,
		"errors":   result.Errors,
	})
}

// approveRevision approves the review gate for one revision. Only the latest
// revision can be approved, because approving an older snapshot would execute
// a graph that no longer represents the plan's current intent.
func (r *planRuntime) approveRevision(c *gin.Context) {
	r.reviewRevision(c, true)
}

func (r *planRuntime) rejectRevision(c *gin.Context) {
	r.reviewRevision(c, false)
}

func (r *planRuntime) reviewRevision(c *gin.Context, approve bool) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	number, ok := parseRevisionParam(c)
	if !ok {
		return
	}
	store := r.revisionStore()
	latest, err := store.Latest(plan.ID)
	if err != nil {
		if errors.Is(err, revision.ErrNotFound) {
			latest, err = r.ensureInitialRevision(plan)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
			return
		}
	}
	if latest.Revision != number {
		c.JSON(http.StatusConflict, gin.H{
			"error":            "only the latest revision can be reviewed",
			"code":             "revision_not_latest",
			"current_revision": latest.Revision,
		})
		return
	}
	now := time.Now().UTC()
	actor := resolveUserID(c)
	status := models.RevisionRejected
	reviewStatus := models.ReviewRejected
	if approve {
		status = models.RevisionApproved
		reviewStatus = models.ReviewApproved
	}
	if plan.Status == models.StatusInProgress || plan.Status == models.StatusAwaitingInput || plan.Status == models.StatusAwaitingApproval {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running or waiting; cannot review now", "code": "plan_running"})
		return
	}
	if err := store.UpdateStatus(plan.ID, number, status, actor, &now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	eventType := "plan_reviewed"
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Review.Status = reviewStatus
		current.Review.ReviewedBy = actor
		current.Review.ReviewedAt = &now
		if approve {
			// Preserve whether the gate was required in the first place: manually
			// approving a simple plan must not turn every later edit into a review.
			current.Status = models.StatusPending
		} else {
			current.Status = models.StatusAwaitingReview
		}
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "plan_not_found"})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: eventType,
		Revision:  number,
		Payload:   map[string]any{"revision": number, "decision": reviewStatus, "actor": actor},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)
	recordAuditEvent("plan_reviewed", actor, plan.ID, map[string]any{"revision": number, "decision": reviewStatus})
	c.JSON(http.StatusOK, gin.H{"revision": number, "status": status, "review_status": reviewStatus})
}

type executeRevisionPayload struct {
	Trigger string `json:"trigger,omitempty"`
}

// executeRevision switches the live plan to a stored revision snapshot and
// starts execution. The revision graph is reset to a runnable state so a
// revision can be executed again after a failed or canceled run.
func (r *planRuntime) executeRevision(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	number, ok := parseRevisionParam(c)
	if !ok {
		return
	}
	var payload executeRevisionPayload
	_ = c.ShouldBindJSON(&payload)
	trigger := strings.TrimSpace(payload.Trigger)
	if trigger == "" {
		trigger = "manual"
	}
	if trigger != "manual" && trigger != "auto" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trigger must be manual or auto", "code": "invalid_trigger"})
		return
	}
	if plan.Status == models.StatusInProgress || plan.Status == models.StatusAwaitingApproval || plan.Status == models.StatusAwaitingInput {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running or waiting; cancel or finish before executing a revision", "code": "plan_running"})
		return
	}
	if plan.Paused {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is paused; resume it before running", "code": "plan_paused"})
		return
	}
	store := r.revisionStore()
	rev, err := store.Get(plan.ID, number)
	if err != nil {
		if errors.Is(err, revision.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "revision not found", "code": "revision_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	if rev.Graph == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "revision has no graph snapshot", "code": "revision_empty"})
		return
	}
	if plan.Review.Required && plan.Review.Status == models.ReviewRejected {
		c.JSON(http.StatusConflict, gin.H{"error": "plan review was rejected; approve a revision before running", "code": "review_rejected"})
		return
	}
	// Status is authoritative even if an older draft tried to clear the review
	// state: a plan waiting in the review queue can only run an approved revision.
	if plan.Status == models.StatusAwaitingReview && plan.Review.Status != models.ReviewApproved && rev.Status != models.RevisionApproved {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is awaiting review; approve it before running", "code": "review_pending"})
		return
	}
	// Each revision carries its own approval: approving r3 must not authorize
	// running an older, never-reviewed snapshot.
	if plan.Review.Required && rev.Status != models.RevisionApproved {
		c.JSON(http.StatusConflict, gin.H{"error": "plan revision is awaiting review; approve it before running", "code": "review_pending"})
		return
	}
	// Reset execution state on a deep copy before applying it.
	resetGraph := *rev.Graph
	resetGraph.Nodes = make([]*models.TaskNode, 0, len(rev.Graph.Nodes))
	now := time.Now().UTC()
	for _, node := range rev.Graph.Nodes {
		if node == nil {
			resetGraph.Nodes = append(resetGraph.Nodes, nil)
			continue
		}
		copyNode := *node
		copyNode.Status = models.StatusPending
		copyNode.Result = ""
		copyNode.Error = ""
		copyNode.Code = ""
		copyNode.StructuredData = ""
		copyNode.ImageBase64 = ""
		copyNode.ExecutionID = ""
		copyNode.ExecutionEpoch = 0
		copyNode.LeaseOwner = ""
		copyNode.LeaseExpiresAt = nil
		copyNode.StartedAt = nil
		copyNode.FinishedAt = nil
		copyNode.RunCount = 0
		copyNode.Usage = nil
		copyNode.Revision = number
		copyNode.UpdatedAt = now
		resetGraph.Nodes = append(resetGraph.Nodes, &copyNode)
	}
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current.Status == models.StatusInProgress {
			return fmt.Errorf("plan is already running")
		}
		current.Nodes = resetGraph.Nodes
		current.Edges = resetGraph.Edges
		current.Budget = resetGraph.Budget
		current.RuntimePolicy = resetGraph.RuntimePolicy
		current.CurrentRevision = number
		current.Review.Status = models.ReviewApproved
		current.Review.Required = resetGraph.Review.Required || current.Review.Required
		current.Status = models.StatusPending
		current.Outcome = ""
		current.OutcomeReason = ""
		current.MissingRequirements = nil
		current.Paused = false
		current.PausedAt = nil
		current.Usage.FinishedAt = nil
		current.UpdatedAt = now
		fillMetaForRuntime(current)
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "plan_running"})
		return
	}
	actor := resolveUserID(c)
	if err := r.launchPlan(plan.ID, trigger, actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running", "code": "plan_already_running"})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_revision_executed",
		Revision:  number,
		Payload:   map[string]any{"revision": number, "actor": actor, "trigger": trigger},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)
	recordAuditEvent("plan_revision_executed", actor, plan.ID, map[string]any{"revision": number, "trigger": trigger})
	c.JSON(http.StatusAccepted, gin.H{
		"message":  "Plan revision execution started",
		"plan_id":  plan.ID,
		"revision": number,
		"trigger":  trigger,
	})
}

// fillMetaForRuntime recomputes the graph counters after replacing nodes.
func fillMetaForRuntime(plan *models.PlanGraph) {
	if plan == nil {
		return
	}
	meta := models.GraphMeta{
		TotalNodes:            len(plan.Nodes),
		PlannerSource:         plan.Meta.PlannerSource,
		PlannerFallbackReason: plan.Meta.PlannerFallbackReason,
		PlanningRoute:         plan.Meta.PlanningRoute,
		Complexity:            plan.Meta.Complexity,
		ComplexitySource:      plan.Meta.ComplexitySource,
		TemplateVariant:       plan.Meta.TemplateVariant,
		GeneralPlanShape:      plan.Meta.GeneralPlanShape,
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusCompleted:
			meta.CompletedNodes++
		case models.StatusFailed:
			meta.FailedNodes++
		case models.StatusBlocked:
			meta.BlockedNodes++
		case models.StatusInProgress:
			meta.InProgressNodes++
		case models.StatusReady:
			meta.ReadyNodes++
		}
	}
	plan.Meta = meta
}

func (r *planRuntime) getPlanDiff(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	from, err := strconv.Atoi(c.Query("from"))
	if err != nil || from <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must be a positive revision", "code": "invalid_revision"})
		return
	}
	to, err := strconv.Atoi(c.Query("to"))
	if err != nil || to <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be a positive revision", "code": "invalid_revision"})
		return
	}
	if _, err := r.ensureInitialRevision(plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
		return
	}
	fromRev, err := r.revisionStore().Get(plan.ID, from)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "from revision not found", "code": "revision_not_found"})
		return
	}
	toRev, err := r.revisionStore().Get(plan.ID, to)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "to revision not found", "code": "revision_not_found"})
		return
	}
	diff := diffPlanGraphs(fromRev.Graph, toRev.Graph)
	diff.From = from
	diff.To = to
	c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "diff": diff.Diff})
}

// effectiveRevisionStatus derives the display status of one revision from the
// plan's execution state. The revision store is append-only, so statuses such
// as executing/completed are read-time projections of the bound revision.
func effectiveRevisionStatus(plan *models.PlanGraph, rev *models.PlanRevision) string {
	if plan == nil || rev == nil {
		return ""
	}
	if plan.CurrentRevision != rev.Revision {
		return rev.Status
	}
	switch plan.Status {
	case models.StatusInProgress:
		return models.RevisionExecuting
	case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
		return models.RevisionCompleted
	}
	return rev.Status
}

// bindLatestRevision records which immutable snapshot the live plan is about to
// execute. Draft edits apply to the live graph, so normal execution must stamp
// that graph with the latest revision instead of leaving a stale revision id.
func (r *planRuntime) bindLatestRevision(planID string) (int, error) {
	plan, err := r.store.GetPlan(planID)
	if err != nil {
		return 0, err
	}
	latest, err := r.revisionStore().Latest(planID)
	if err != nil {
		if !errors.Is(err, revision.ErrNotFound) {
			return 0, err
		}
		latest, err = r.ensureInitialRevision(plan)
		if err != nil {
			return 0, err
		}
	}
	if latest == nil {
		return 0, nil
	}
	err = r.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
		current.CurrentRevision = latest.Revision
		for _, node := range current.Nodes {
			if node != nil {
				node.Revision = latest.Revision
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return latest.Revision, nil
}

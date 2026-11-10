package api

import (
	"encoding/json"
	"errors"
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
// revision is mandatory and must match the latest revision, which provides
// optimistic locking: two clients editing the same revision cannot silently
// overwrite each other.
//
// Everything that can reject the edit (running/paused state, an accepted launch,
// a stale base) is decided inside the plan store's UpdatePlan callback. Only
// after the revision is durably created is the live graph replaced, so a
// rejected edit leaves neither a revision behind nor a half-applied graph.
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
	if payload.BaseRevision <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "base_revision is required and must be a positive revision number",
			"code":  "invalid_base_revision",
		})
		return
	}

	unlock := r.lockPlanRevisionMutation(plan.ID)
	defer unlock()

	// Everything below acts on a plan re-read while holding the lock; the
	// snapshot from authorizedPlan only decided ownership.
	if fresh, err := r.store.GetPlan(plan.ID); err != nil || fresh == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	} else {
		plan = fresh
	}
	// Editing a running or paused plan is never allowed; it would make the
	// executing revision ambiguous. The authoritative re-check happens again
	// inside UpdatePlan.
	if guard := checkPlanRevisionEditable(plan, "plan is running or waiting; cancel or finish before editing"); guard != nil {
		respondRevisionMutationError(c, guard)
		return
	}
	// Lazy migration happens under the lock so two editors cannot both create
	// revision 1, and from a freshly read plan so the initial snapshot is real.
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

	now := time.Now().UTC()
	actor := resolveUserID(c)
	var created *models.PlanRevision
	err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current == nil {
			return newRevisionGuard(http.StatusNotFound, "plan_not_found", "plan not found")
		}
		// Authoritative state check: the snapshot from authorizedPlan may be
		// stale, and a disabled button in the UI is not a concurrency control.
		if guard := checkPlanRevisionEditable(current, "plan is running or waiting; cancel or finish before editing"); guard != nil {
			return guard
		}
		if r.planExecutionAccepted(plan.ID) {
			return newRevisionGuard(http.StatusConflict, "plan_running", "plan is already running; cancel or finish before editing")
		}
		// Server-owned identity and gate state are stamped from the state the
		// store just read, not from the possibly stale request snapshot.
		prepareRevisionDraft(current, payload.Graph)
		previous, _ := r.revisionStore().Latest(plan.ID)
		rev, createErr := r.revisionStore().Create(&models.PlanRevision{
			PlanID:    plan.ID,
			Graph:     payload.Graph,
			Status:    models.RevisionDraft,
			CreatedBy: actor,
			Reason:    strings.TrimSpace(payload.Reason),
		}, payload.BaseRevision)
		if createErr != nil {
			return createErr
		}
		if previous != nil {
			rev.DiffSummary = diffSummary(diffPlanGraphs(previous.Graph, payload.Graph))
		}
		// The snapshot is durable; only now may the live graph change.
		applyDraftGraph(current, payload.Graph)
		current.UpdatedAt = now
		created = rev
		return nil
	})
	if err != nil {
		respondRevisionMutationError(c, err)
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_revision_created",
		Revision:  created.Revision,
		Payload: map[string]any{
			"revision":        created.Revision,
			"parent_revision": created.ParentRevision,
			"actor":           actor,
			"reason":          created.Reason,
			"diff_summary":    created.DiffSummary,
		},
		Timestamp: now,
	}
	if err := r.persistAndPublishPlanEvent(plan.ID, event); err != nil {
		respondEventPersistFailure(c, err)
		return
	}
	recordAuditEvent("plan_revision_created", actor, plan.ID, map[string]any{"revision": created.Revision, "parent_revision": created.ParentRevision})
	updated, _ := r.store.GetPlan(plan.ID)
	draft := summarizePlan(updated)
	c.JSON(http.StatusOK, gin.H{"revision": summarizeRevision(created), "plan": draft})
}

// prepareRevisionDraft stamps the server-owned identity and gate state of the
// authoritative plan onto a client-edited draft graph.
func prepareRevisionDraft(current, draft *models.PlanGraph) {
	if current == nil || draft == nil {
		return
	}
	// The edited graph must keep the plan identity and ownership; everything
	// else is the user's to change.
	draft.ID = current.ID
	draft.OwnerID = current.OwnerID
	draft.SessionID = current.SessionID
	draft.TraceID = current.TraceID
	draft.CreatedAt = current.CreatedAt
	if draft.Artifacts == nil {
		draft.Artifacts = current.Artifacts
	}
	// The review and runtime-approval gates are server-owned. A client edit may
	// change the graph but must never clear a required review or silently
	// inherit an earlier approval: the new draft needs its own review decision.
	draft.Approval = current.Approval
	if current.Review.Required || current.Status == models.StatusAwaitingReview {
		draft.Review = models.ReviewState{
			Required:     true,
			Status:       models.ReviewPending,
			Reason:       current.Review.Reason,
			Risk:         current.Review.Risk,
			EstimatedUSD: current.Review.EstimatedUSD,
		}
	} else {
		draft.Review = current.Review
	}
}

// applyDraftGraph copies the editable graph fields into the live plan while
// preserving execution state and history that the draft does not own.
func applyDraftGraph(current, draft *models.PlanGraph) {
	if current == nil || draft == nil {
		return
	}
	preserveRecovery(current.Nodes, draft.Nodes)
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

// Recovery is runtime-owned and survives edits or execution of an old snapshot.
// A client-supplied graph cannot replenish (or fabricate) its allowance/ledger.
func preserveRecovery(current, next []*models.TaskNode) {
	byID := map[string]*models.RecoveryState{}
	for _, node := range current {
		if node != nil {
			byID[node.ID] = node.Recovery
		}
	}
	for _, node := range next {
		if node == nil {
			continue
		}
		node.Recovery = nil
		if previous := byID[node.ID]; previous != nil {
			raw, _ := json.Marshal(previous)
			_ = json.Unmarshal(raw, &node.Recovery)
		}
	}
}

func copyRuntimeMetadata(metadata map[string]any) map[string]any {
	copy := make(map[string]any, len(metadata))
	for key, value := range metadata {
		switch key {
		case "completed_execution_id", "completed_execution_epoch", "completed_revision", "agent_run", "runtime_action":
			continue
		}
		copy[key] = value
	}
	return copy
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
	unlock := r.lockPlanRevisionMutation(plan.ID)
	defer unlock()

	if fresh, err := r.store.GetPlan(plan.ID); err != nil || fresh == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	} else {
		plan = fresh
	}
	store := r.revisionStore()
	if _, err := store.Latest(plan.ID); err != nil {
		if !errors.Is(err, revision.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
			return
		}
		if _, err := r.ensureInitialRevision(plan); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
			return
		}
	}
	// Fast failure on the locked snapshot; UpdatePlan re-checks below.
	if guard := checkPlanRevisionEditable(plan, "plan is running or waiting; cannot review now"); guard != nil {
		respondRevisionMutationError(c, guard)
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
	// The approval decision and the live review state change inside the same
	// plan store callback: a rejection therefore never leaves the revision
	// approved (or vice versa), and the revision the caller named must still be
	// the latest one.
	err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current == nil {
			return newRevisionGuard(http.StatusNotFound, "plan_not_found", "plan not found")
		}
		if guard := checkPlanRevisionEditable(current, "plan is running or waiting; cannot review now"); guard != nil {
			return guard
		}
		if r.planExecutionAccepted(plan.ID) {
			return newRevisionGuard(http.StatusConflict, "plan_running", "plan is already running; cannot review now")
		}
		latest, latestErr := store.Latest(plan.ID)
		if latestErr != nil {
			return latestErr
		}
		if latest.Revision != number {
			return newRevisionGuard(http.StatusConflict, "revision_not_latest", "only the latest revision can be reviewed").
				with("current_revision", latest.Revision)
		}
		if err := store.UpdateStatus(plan.ID, number, status, actor, &now); err != nil {
			return err
		}
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
	})
	if err != nil {
		respondRevisionMutationError(c, err)
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_reviewed",
		Revision:  number,
		Payload:   map[string]any{"revision": number, "decision": reviewStatus, "actor": actor},
		Timestamp: now,
	}
	if err := r.persistAndPublishPlanEvent(plan.ID, event); err != nil {
		respondEventPersistFailure(c, err)
		return
	}
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
	unlock := r.lockPlanRevisionMutation(plan.ID)
	defer unlock()

	if fresh, err := r.store.GetPlan(plan.ID); err != nil || fresh == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	} else {
		plan = fresh
	}
	// Fast failure on the locked snapshot; the authoritative re-check happens
	// inside UpdatePlan.
	if guard := checkPlanRevisionEditable(plan, "plan is running or waiting; cancel or finish before executing a revision"); guard != nil {
		respondRevisionMutationError(c, guard)
		return
	}

	// Read the snapshot under the lock: a revision created concurrently must not
	// be applied from a half-stale read.
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
	// Each revision carries its own approval: approving r3 must not authorize
	// running an older, never-reviewed snapshot. That check needs the
	// authoritative review state, so it runs inside the store callback below.
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
		copyNode.Metadata = copyRuntimeMetadata(node.Metadata)
		copyNode.Failure = nil
		copyNode.NextAttemptAt = nil
		copyNode.ReplanProposal = nil
		copyNode.Revision = number
		copyNode.UpdatedAt = now
		resetGraph.Nodes = append(resetGraph.Nodes, &copyNode)
	}

	// The status, pause and launch-window checks happen inside the callback so
	// they see the state the store holds now, not the request's snapshot. A plan
	// the scheduler already accepted is rejected here instead of having its live
	// graph reset underneath the running execution.
	err = r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current == nil {
			return newRevisionGuard(http.StatusNotFound, "plan_not_found", "plan not found")
		}
		if guard := checkPlanRevisionEditable(current, "plan is running or waiting; cancel or finish before executing a revision"); guard != nil {
			return guard
		}
		if r.planExecutionAccepted(plan.ID) {
			return newRevisionGuard(http.StatusConflict, "plan_running", "plan was already started; cancel or finish before executing a revision")
		}
		if current.Review.Required && current.Review.Status == models.ReviewRejected {
			return newRevisionGuard(http.StatusConflict, "review_rejected", "plan review was rejected; approve a revision before running")
		}
		// Status is authoritative even if an older draft tried to clear the
		// review state: a plan waiting in the review queue can only run an
		// approved revision.
		if current.Status == models.StatusAwaitingReview && current.Review.Status != models.ReviewApproved && rev.Status != models.RevisionApproved {
			return newRevisionGuard(http.StatusConflict, "review_pending", "plan is awaiting review; approve it before running")
		}
		if current.Review.Required && rev.Status != models.RevisionApproved {
			return newRevisionGuard(http.StatusConflict, "review_pending", "plan revision is awaiting review; approve it before running")
		}
		preserveRecovery(current.Nodes, resetGraph.Nodes)
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
	})
	if err != nil {
		respondRevisionMutationError(c, err)
		return
	}
	actor := resolveUserID(c)
	if err := r.launchPlan(plan.ID, trigger, actor); err != nil {
		respondPlanLaunchError(c, err)
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_revision_executed",
		Revision:  number,
		Payload:   map[string]any{"revision": number, "actor": actor, "trigger": trigger},
		Timestamp: now,
	}
	if err := r.persistAndPublishPlanEvent(plan.ID, event); err != nil {
		respondEventPersistFailure(c, err)
		return
	}
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

// Package recovery shares one persisted allowance between every recovery layer.
package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/models"
)

type contextKey struct{}
type compatibilityKey struct{}
type Compatibility struct {
	EnvironmentHash, ProtocolHash, ExecutionID string
	ProcedureIDs                               []string
}

func WithCompatibility(ctx context.Context, c Compatibility) context.Context {
	return context.WithValue(ctx, compatibilityKey{}, c)
}
func CompatibleContext(ctx context.Context) Compatibility {
	c, _ := ctx.Value(compatibilityKey{}).(Compatibility)
	return c
}

type Controller struct {
	mu      sync.Mutex
	state   models.RecoveryState
	persist func(models.RecoveryState) error
}

func Hash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func New(state models.RecoveryState, persist func(models.RecoveryState) error) *Controller {
	if state.Version == "" {
		state.Version = "recovery/v1"
	}
	if state.ChainID == "" {
		state.ChainID = Hash(time.Now().UnixNano())
	}
	if state.MaxActions <= 0 {
		state.MaxActions = 2
	}
	if state.MaxHTTPAttempts <= 0 {
		state.MaxHTTPAttempts = 64
	}
	return &Controller{state: state, persist: persist}
}
func WithController(ctx context.Context, c *Controller) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}
func FromContext(ctx context.Context) *Controller {
	c, _ := ctx.Value(contextKey{}).(*Controller)
	return c
}
func clone(s models.RecoveryState) models.RecoveryState {
	raw, _ := json.Marshal(s)
	var out models.RecoveryState
	_ = json.Unmarshal(raw, &out)
	return out
}
func (c *Controller) Snapshot() models.RecoveryState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return clone(c.state)
}
func (c *Controller) save(next models.RecoveryState) error {
	if c.persist != nil {
		if err := c.persist(clone(next)); err != nil {
			return err
		}
	}
	c.state = next
	return nil
}

// Reserve commits before dispatching an extra HTTP attempt, repair, or rerun.
// nil controllers preserve embedded/legacy standalone callers.
func Reserve(ctx context.Context, kind string, failure *models.FailureInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := FromContext(ctx)
	if c == nil {
		return nil
	}
	return c.Reserve(kind, failure)
}
func (c *Controller) Reserve(kind string, failure *models.FailureInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := clone(c.state)
	if next.Version != "recovery/v1" {
		return fmt.Errorf("recovery_state_version_unsupported")
	}
	if next.UsedActions >= next.MaxActions {
		return fmt.Errorf("recovery_budget_exhausted")
	}
	if failure != nil {
		if failure.Class == "canceled" || failure.Class == "permission" || failure.Class == "missing_input" || failure.Class == "budget" {
			return fmt.Errorf("recovery_not_permitted: %s", failure.Class)
		}
		if failure.Class != "transient" {
			for _, previous := range next.Failures {
				if previous.Fingerprint == failure.Fingerprint && previous.StateHash == failure.StateHash {
					return fmt.Errorf("recovery_no_progress")
				}
			}
		}
		next.Failures = append(next.Failures, *failure)
	}
	next.UsedActions++
	decision := models.RecoveryDecision{Action: kind, Consumed: 1, Remaining: next.MaxActions - next.UsedActions, At: time.Now().UTC()}
	if failure != nil {
		decision.Reason = failure.Code
		decision.FailureFingerprint = failure.Fingerprint
	}
	next.Decisions = append(next.Decisions, decision)
	return c.save(next)
}
func HTTPAttempt(ctx context.Context, retry bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := FromContext(ctx)
	if c == nil {
		return nil
	}
	if retry {
		if err := c.Reserve("http_retry", &models.FailureInfo{Class: "transient", Code: "http_retry"}); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := clone(c.state)
	if next.HTTPAttempts >= next.MaxHTTPAttempts {
		return fmt.Errorf("model_http_budget_exhausted")
	}
	next.HTTPAttempts++
	return c.save(next)
}
func Record(ctx context.Context, entry models.ActionLedgerEntry) error {
	c := FromContext(ctx)
	if c == nil {
		return nil
	}
	return c.Record(entry)
}
func (c *Controller) Record(entry models.ActionLedgerEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := clone(c.state)
	if entry.ID == "" {
		entry.ID = Hash([]any{next.ChainID, len(next.Ledger), entry})
	}
	for _, old := range next.Ledger {
		if old.ID == entry.ID {
			return nil
		}
	}
	entry.At = time.Now().UTC()
	next.Ledger = append(next.Ledger, entry)
	return c.save(next)
}

var noise = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[tT ][0-9:.+zZ-]+|\b[0-9a-f]{8}-[0-9a-f-]{27,}\b|(?:/tmp/|[A-Za-z]:\\[^ ]*[tT]emp\\)[^\s:]+`)

func Classify(err error, stage, stateHash string) *models.FailureInfo {
	if err == nil {
		return nil
	}
	text := err.Error()
	lower := strings.ToLower(text)
	f := &models.FailureInfo{Class: "deterministic", Code: "execution_failed", Stage: stage, Message: text, StateHash: stateHash}
	var httpFailure interface {
		HTTPStatus() int
		RetryDelay() time.Duration
	}
	if errors.As(err, &httpFailure) {
		f.RetryAfterMS = httpFailure.RetryDelay().Milliseconds()
		status := httpFailure.HTTPStatus()
		if status == 429 || status == 408 || status == 425 || status == 500 || status == 502 || status == 503 || status == 504 {
			f.Class, f.Code, f.Recoverable = "transient", "transient_failure", true
			f.Fingerprint = Hash([]any{f.Class, status, stage, stateHash})
			return f
		}
	}
	switch {
	case errors.Is(err, context.Canceled) || strings.Contains(lower, "canceled"):
		f.Class, f.Code = "canceled", "canceled"
	case strings.Contains(lower, "budget") || strings.Contains(lower, "max_steps") || strings.Contains(lower, "step_limit"):
		f.Class, f.Code = "budget", "budget_exhausted"
	case strings.Contains(lower, "forbidden") || strings.Contains(lower, "approval") || strings.Contains(lower, "permission"):
		f.Class, f.Code = "permission", "permission_required"
	case strings.Contains(lower, "missing dataset") || strings.Contains(lower, "missing input") || strings.Contains(lower, "not found: dataset") || strings.Contains(lower, "missing_requirements"):
		f.Class, f.Code = "missing_input", "input_required"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "transient") || strings.Contains(lower, "temporary") || strings.Contains(lower, "429") || strings.Contains(lower, "503") || strings.Contains(lower, "502") || strings.Contains(lower, "504") || strings.Contains(lower, "connection reset") || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		f.Class, f.Code, f.Recoverable = "transient", "transient_failure", true
	case strings.Contains(lower, "dependency") || strings.Contains(lower, "modulenotfound") || strings.Contains(lower, "no module named") || strings.Contains(lower, "pip install"):
		f.Class, f.Code, f.Recoverable = "environment", "environment_mismatch", true
	case strings.Contains(lower, "schema") || strings.Contains(lower, "json"):
		f.Class, f.Code, f.Recoverable = "output", "output_invalid", true
	}
	f.Fingerprint = Hash([]string{f.Class, f.Code, stage, noise.ReplaceAllString(strings.Join(strings.Fields(lower), " "), "<volatile>"), stateHash})
	return f
}

// RecordVerifiedRepair stores the actual code and execution logs before adding
// the verified ledger entry. Oversized evidence is retained by the caller's
// artifact path; it cannot silently become a verified procedure here.
func RecordVerifiedRepair(ctx context.Context, beforeCode, afterCode, beforeLog, afterLog string) error {
	return recordRepairEvidence(ctx, beforeCode, afterCode, beforeLog, afterLog, true)
}
func RecordFailedRepair(ctx context.Context, beforeCode, afterCode, beforeLog, afterLog string) error {
	return recordRepairEvidence(ctx, beforeCode, afterCode, beforeLog, afterLog, false)
}
func recordRepairEvidence(ctx context.Context, beforeCode, afterCode, beforeLog, afterLog string, verified bool) error {
	c := FromContext(ctx)
	if c == nil {
		return nil
	}
	if beforeCode == afterCode {
		return fmt.Errorf("recovery_no_progress")
	}
	for _, raw := range []string{beforeCode, afterCode, beforeLog, afterLog} {
		if len(raw) > 1<<20 {
			return nil
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := clone(c.state)
	compat := CompatibleContext(ctx)
	id := Hash([]string{next.ChainID, beforeCode, afterCode, beforeLog, afterLog})
	for _, old := range next.Ledger {
		if old.ID == id {
			return nil
		}
	}
	refs := []string{}
	for i, raw := range []string{beforeCode, beforeLog, afterCode, afterLog} {
		key := fmt.Sprintf("repair_%s_%d", id, i)
		next.Evidence = append(next.Evidence, models.Artifact{Key: key, Kind: "repair_evidence", Value: raw, EvidenceClass: "execution_derived", CreatedAt: time.Now().UTC()})
		refs = append(refs, key)
	}
	next.Ledger = append(next.Ledger, models.ActionLedgerEntry{ID: id, Kind: "repair", Action: "replace_code_and_execute_same_protocol", BeforeStateHash: Hash(beforeCode), AfterStateHash: Hash(afterCode), EnvironmentHash: compat.EnvironmentHash, ProtocolHash: compat.ProtocolHash, ExecutionID: compat.ExecutionID, BeforeEvidence: refs[:2], AfterEvidence: refs[2:], ArtifactRefs: refs, Verified: verified, At: time.Now().UTC(), ProcedureIDs: append([]string(nil), compat.ProcedureIDs...)})
	return c.save(next)
}

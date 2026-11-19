package store

import (
	"errors"
	"fmt"
)

var (
	ErrPlanNotFound          = errors.New("plan not found")
	ErrNotCommitted          = errors.New("plan store write not committed")
	ErrCommitUnknown         = errors.New("plan store commit result unknown")
	ErrStoreRecoveryRequired = errors.New("plan store requires recovery")
)

// CommitError reports a durable write whose result cannot safely be retried.
// Reopen and recover the store before issuing another operation.
type CommitError struct {
	Operation  string
	PlanID     string
	Generation string
	Seq        int64
	Cause      error
}

func (e *CommitError) Error() string {
	return fmt.Sprintf("%s: operation=%s plan=%s generation=%s seq=%d: %v", ErrCommitUnknown, e.Operation, e.PlanID, e.Generation, e.Seq, e.Cause)
}
func (e *CommitError) Unwrap() error        { return e.Cause }
func (e *CommitError) Is(target error) bool { return target == ErrCommitUnknown }

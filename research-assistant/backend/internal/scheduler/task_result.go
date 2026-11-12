package scheduler

import (
	"fmt"

	"research-assistant-backend/internal/models"
)

// taskResultClass is the scheduler-facing classification of one execution
// result. Dispatch and the manual single-node entry point share it so neither
// can keep its own "anything but completed is a failure" rule.
type taskResultClass int

const (
	resultUnknown taskResultClass = iota
	resultCompleted
	resultWaitingApproval
	resultWaitingInput
	resultBlocked
	resultFailed
	resultCanceled
)

func isFailureClass(class taskResultClass) bool {
	switch class {
	case resultBlocked, resultFailed, resultCanceled:
		return true
	default:
		return false
	}
}

// classifyTaskResult maps a normalized result plus the execution error onto the
// scheduler classes. The exception order is fixed:
//  1. an explicit execution error (canceled / timed out / executor error),
//  2. keyed execution states the runner reported before returning,
//  3. question / runtime action (waits),
//  4. completed,
//  5. explicit blocked / canceled / failed statuses,
//  6. anything else is a protocol error, never a silent success or wait.
func classifyTaskResult(result *models.TaskExecutionResult, execErr error) taskResultClass {
	if result == nil {
		return resultFailed
	}
	if execErr != nil {
		// The execution exception decides: a status the runner set before the error
		// is only kept when it is itself a stop, and the strongest stop wins.
		class := statusClass(result.Status)
		if isFailureClass(class) && exceptionRank(class) > 0 {
			return class
		}
		return resultFailed
	}
	if class := statusClass(result.Status); isFailureClass(class) || isWaitingClass(class) || class == resultCompleted {
		return class
	}
	// Pending/ready/in-progress with no wait record, and an empty status, are
	// ambiguous: the executor did not say what happened. Recording that as a
	// completion would accept work that never ran, and recording it as a wait
	// would strand the plan, so it is an explicit protocol error.
	switch result.Status {
	case models.StatusPending, models.StatusReady, models.StatusInProgress, "":
		if result.RuntimeAction != nil {
			return resultWaitingApproval
		}
		if result.Question != nil {
			return resultWaitingInput
		}
		return resultUnknown
	case models.StatusCompleted, models.StatusSkipped:
		return resultCompleted
	}
	return resultUnknown
}

func isWaitingClass(class taskResultClass) bool {
	return class == resultWaitingApproval || class == resultWaitingInput
}

// exceptionRank orders the execution exceptions the scheduler must keep ahead
// of a waiting status: a canceled or timed-out execution never becomes a normal
// wait just because it also carried a question or a runtime action. It is the
// documented priority order used by classifyTaskResult and by the tests.
func exceptionRank(class taskResultClass) int {
	switch class {
	case resultCanceled:
		return 3
	case resultBlocked:
		return 2
	case resultFailed:
		return 1
	default:
		return 0
	}
}

func isCanceledStatus(status models.TaskStatus) bool {
	return status == models.StatusCanceled
}

func statusClass(status models.TaskStatus) taskResultClass {
	switch status {
	case models.StatusAwaitingApproval:
		return resultWaitingApproval
	case models.StatusAwaitingInput:
		return resultWaitingInput
	case models.StatusBlocked:
		return resultBlocked
	case models.StatusCanceled:
		return resultCanceled
	case models.StatusFailed:
		return resultFailed
	case models.StatusCompleted, models.StatusSkipped:
		return resultCompleted
	default:
		return resultUnknown
	}
}

// errTaskProtocol is returned when a result cannot be interpreted. It is a
// protocol error: recording it as a wait would strand the plan, and recording
// it as success would accept work that never ran.
var errTaskProtocol = fmt.Errorf("task result protocol error")

func protocolError(detail string) error {
	return fmt.Errorf("%w: %s", errTaskProtocol, detail)
}

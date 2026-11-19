package agent

import (
	"errors"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func isSandboxSessionID(id string) bool {
	for _, prefix := range []string{"dk-", "os-", "nt-", "vm-"} {
		if strings.HasPrefix(id, prefix) && len(id) > len(prefix) {
			return true
		}
	}
	return false
}

func isSandboxCapabilityError(err error) bool {
	var capability *sandbox.CapabilityError
	return errors.As(err, &capability)
}

// A blocked capability result must return nil as an execution error: the
// scheduler deliberately turns non-nil errors into failed task results.
func applySandboxCapabilityBlock(task *models.Task, err error) bool {
	var capability *sandbox.CapabilityError
	if task == nil || !errors.As(err, &capability) {
		return false
	}
	task.Status = models.StatusBlocked
	task.Error = capability.Error()
	task.Failure = nil
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["missing_requirements"] = append([]models.MissingRequirement(nil), capability.MissingRequirements...)
	return true
}

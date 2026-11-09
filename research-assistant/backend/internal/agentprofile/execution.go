package agentprofile

import (
	"fmt"
	"research-assistant-backend/internal/models"
)

func NormalizeExecution(profile *models.AgentProfile) error {
	if profile.ExecutionMode == "" {
		profile.ExecutionMode = "single"
	}
	if profile.ExecutionMode != "single" && profile.ExecutionMode != "tool_loop" {
		return fmt.Errorf("%w: unsupported execution_mode", ErrInvalid)
	}
	if profile.MaxSteps == 0 {
		profile.MaxSteps = 6
	}
	if profile.MaxToolCalls == 0 {
		profile.MaxToolCalls = 12
	}
	if profile.MaxSteps < 1 || profile.MaxSteps > 64 || profile.MaxToolCalls < 1 || profile.MaxToolCalls > 128 {
		return fmt.Errorf("%w: execution limits out of range", ErrInvalid)
	}
	return nil
}

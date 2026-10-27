package agent

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
	"research-assistant-backend/internal/usage"
)

// sandboxNetworkController is implemented by the real sandbox client; test
// doubles simply omit it and network control becomes a no-op.
type sandboxNetworkController interface {
	SetSandboxNetwork(ctx context.Context, sandboxID, mode string) error
}

// runtimePolicyForTask reads the RuntimePolicy propagated by the scheduler so
// agents can enforce the same hard limits as the sandbox service.
func runtimePolicyForTask(task *models.Task) models.RuntimePolicy {
	if task == nil || task.Metadata == nil {
		return models.RuntimePolicy{}
	}
	switch value := task.Metadata["runtime_policy"].(type) {
	case models.RuntimePolicy:
		return value
	case *models.RuntimePolicy:
		if value != nil {
			return *value
		}
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err == nil {
			var policy models.RuntimePolicy
			if json.Unmarshal(encoded, &policy) == nil {
				return policy
			}
		}
	}
	return models.RuntimePolicy{}
}

// sandboxOptionsForTask converts the plan policy into sandbox limits. The
// default network mode is always "none"; callers open egress explicitly for an
// approved install or execution.
func sandboxOptionsForTask(task *models.Task) sandbox.SandboxOptions {
	policy := runtimePolicyForTask(task)
	limits := sandbox.SandboxLimits{
		MaxImageBytes:    policy.HardLimits.MaxImageBytes,
		MaxDownloadBytes: policy.HardLimits.MaxDownloadBytes,
		MaxDiskGB:        policy.HardLimits.MaxDiskGB,
		MaxRuntimeSec:    policy.HardLimits.MaxRuntimeSec,
	}
	runtimeName := strings.TrimSpace(os.Getenv("SANDBOX_RUNTIME"))
	return sandbox.SandboxOptions{NetworkMode: "none", Runtime: runtimeName, Limits: limits}
}

// taskNeedsNetwork reports whether the scheduler approved network access for
// this task through a runtime action.
func taskNeedsNetwork(task *models.Task) bool {
	if task == nil || task.Metadata == nil {
		return false
	}
	value, ok := task.Metadata["network_access"].(bool)
	return ok && value
}

// taskRuntimeSession returns the sandbox id bound to a task's inputs.
func taskRuntimeSession(task *models.Task) string {
	if task == nil {
		return ""
	}
	for _, key := range []string{"prepared_runtime", "runtime_session", "runtime_env"} {
		if task.Inputs != nil {
			if value, ok := task.Inputs[key]; ok && value != nil {
				if text := strings.TrimSpace(toString(value)); text != "" {
					return text
				}
			}
		}
		if task.Metadata != nil {
			if value, ok := task.Metadata["artifact_values"].(map[string]string); ok {
				if text := strings.TrimSpace(value[key]); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

// openSandboxNetwork enables egress for a sandbox that was created with
// network=none. It returns a close function that always restores the offline
// state, even when the command fails.
func openSandboxNetwork(ctx context.Context, controller any, sandboxID, mode string) (func(), error) {
	closeFn := func() {}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" || !strings.HasPrefix(sandboxID, "dk-") {
		return closeFn, nil
	}
	ctrl, ok := controller.(sandboxNetworkController)
	if !ok {
		return closeFn, nil
	}
	if mode == "" {
		mode = "bridge"
	}
	if err := ctrl.SetSandboxNetwork(ctx, sandboxID, mode); err != nil {
		return nil, err
	}
	return func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ctrl.SetSandboxNetwork(closeCtx, sandboxID, "none"); err != nil {
			log.Printf("[sandbox] failed to close network for %s: %v", sandboxID, err)
		}
	}, nil
}

// recordSandboxDuration attributes one sandbox command to the task usage ledger.
func recordSandboxDuration(ctx context.Context, started time.Time) {
	if recorder := usage.FromContext(ctx); recorder != nil {
		recorder.RecordSandbox(time.Since(started).Seconds())
	}
}

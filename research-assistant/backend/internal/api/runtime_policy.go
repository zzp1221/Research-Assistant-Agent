package api

import (
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func buildRuntimePolicy(payload RequestPayload, userID string) (models.RuntimePolicy, error) {
	mode := strings.TrimSpace(payload.RuntimePermissionMode)
	if mode == "" {
		mode = string(models.RuntimePermissionConfirmImportant)
	}
	if mode != string(models.RuntimePermissionConfirmImportant) && mode != string(models.RuntimePermissionFullAccess) {
		return models.RuntimePolicy{}, fmt.Errorf("invalid runtime_permission_mode %q", mode)
	}
	policy := models.RuntimePolicy{
		Mode:        models.RuntimePermissionMode(mode),
		RequestedBy: userID,
		RequestedAt: time.Now(),
		HardLimits: models.RuntimeHardLimits{
			MaxImageBytes:     8 * 1024 * 1024 * 1024,
			MaxDownloadBytes:  8 * 1024 * 1024 * 1024,
			MaxDiskGB:         50,
			MaxRuntimeSec:     3600,
			AllowedRegistries: []string{"docker.io", "registry-1.docker.io", "ghcr.io"},
		},
	}
	if policy.Mode == models.RuntimePermissionFullAccess {
		if !payload.RuntimePermissionAcknowledged {
			return models.RuntimePolicy{}, fmt.Errorf("runtime_permission_mode=full_access requires runtime_permission_acknowledged=true")
		}
		now := time.Now()
		policy.AcknowledgedAt = &now
	}
	return policy, nil
}

package sandbox

import (
	"encoding/json"
	"fmt"
	"net/http"

	"research-assistant-backend/internal/models"
)

// CapabilityError is returned only for the service's explicit blocked contract.
// A provider/transport/install error is never classified by matching its text.
type CapabilityError struct {
	Message             string
	MissingRequirements []models.MissingRequirement
}

func (e *CapabilityError) Error() string { return e.Message }

func sandboxCreateError(status int, body []byte) error {
	return sandboxResponseError(status, body, "沙箱服务返回错误")
}

func sandboxResponseError(status int, body []byte, action string) error {
	var response struct {
		Status              string                      `json:"status"`
		Code                string                      `json:"code"`
		Error               string                      `json:"error"`
		MissingRequirements []models.MissingRequirement `json:"missing_requirements"`
	}
	if status == http.StatusConflict && json.Unmarshal(body, &response) == nil &&
		response.Status == "blocked" && response.Code == "sandbox_capability" && response.Error != "" && len(response.MissingRequirements) > 0 {
		valid := true
		for _, missing := range response.MissingRequirements {
			if missing.Kind != "sandbox_capability" || missing.Detail == "" || missing.Remediation == "" {
				valid = false
			}
		}
		if valid {
			return &CapabilityError{Message: response.Error, MissingRequirements: response.MissingRequirements}
		}
	}
	return fmt.Errorf("%s (Status %d): %s", action, status, string(body))
}

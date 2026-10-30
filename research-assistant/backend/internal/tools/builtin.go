package tools

import (
	"time"

	"research-assistant-backend/internal/models"
)

// BuiltinToolDefinitions are the code-owned tools that make the minimum
// closed loop work: a read-only echo, a guarded file writer, a failing tool,
// a sleeper and an oversized-output tool. They are global (OwnerID="") and
// only an administrator may mutate them.
func BuiltinToolDefinitions() []models.ToolDefinition {
	now := time.Now().UTC()
	return []models.ToolDefinition{
		{
			ID:             "native:echo",
			Name:           "Echo",
			Version:        "1.0.0",
			Transport:      models.ToolTransportNative,
			Description:    "Return the message unchanged. Read-only and safe for the minimum tool loop.",
			InputSchema:    `{"type":"object","properties":{"message":{"type":"string","minLength":1,"maxLength":8192}},"required":["message"],"additionalProperties":false}`,
			OutputSchema:   `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
			Scopes:         []string{},
			SideEffect:     false,
			Approval:       models.ToolApprovalNever,
			TimeoutSec:     10,
			MaxOutputBytes: 64 << 10,
			Enabled:        true,
			Builtin:        true,
			Native:         "echo",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "native:write_file",
			Name:           "Write file",
			Version:        "1.0.0",
			Transport:      models.ToolTransportNative,
			Description:    "Write a UTF-8 file inside the tool workspace. Side-effecting and approval-gated.",
			InputSchema:    `{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":512},"content":{"type":"string","maxLength":1048576}},"required":["path","content"],"additionalProperties":false}`,
			OutputSchema:   `{"type":"object","properties":{"path":{"type":"string"},"bytes":{"type":"integer"}},"required":["path","bytes"]}`,
			Scopes:         []string{"fs.write"},
			SideEffect:     true,
			Approval:       models.ToolApprovalWrite,
			TimeoutSec:     10,
			MaxOutputBytes: 64 << 10,
			Enabled:        true,
			Builtin:        true,
			Native:         "write_file",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "native:fail",
			Name:           "Fail",
			Version:        "1.0.0",
			Transport:      models.ToolTransportNative,
			Description:    "Always fail with a structured error. Used to exercise failure handling.",
			InputSchema:    `{"type":"object","properties":{"message":{"type":"string"}},"additionalProperties":false}`,
			Scopes:         []string{},
			SideEffect:     false,
			Approval:       models.ToolApprovalNever,
			TimeoutSec:     10,
			MaxOutputBytes: 64 << 10,
			Enabled:        true,
			Builtin:        true,
			Native:         "fail",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "native:sleep",
			Name:           "Sleep",
			Version:        "1.0.0",
			Transport:      models.ToolTransportNative,
			Description:    "Sleep for the requested milliseconds. Used to exercise timeouts.",
			InputSchema:    `{"type":"object","properties":{"ms":{"type":"integer","minimum":1,"maximum":60000}},"required":["ms"],"additionalProperties":false}`,
			Scopes:         []string{},
			SideEffect:     false,
			Approval:       models.ToolApprovalNever,
			TimeoutSec:     5,
			MaxOutputBytes: 64 << 10,
			Enabled:        true,
			Builtin:        true,
			Native:         "sleep",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "native:big_output",
			Name:           "Big output",
			Version:        "1.0.0",
			Transport:      models.ToolTransportNative,
			Description:    "Return N bytes of text. Used to exercise MaxOutputBytes truncation.",
			InputSchema:    `{"type":"object","properties":{"bytes":{"type":"integer","minimum":1,"maximum":10485760}},"required":["bytes"],"additionalProperties":false}`,
			Scopes:         []string{},
			SideEffect:     false,
			Approval:       models.ToolApprovalNever,
			TimeoutSec:     10,
			MaxOutputBytes: 1024,
			Enabled:        true,
			Builtin:        true,
			Native:         "big_output",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

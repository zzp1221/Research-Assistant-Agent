package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Tool transports supported by the tool registry.
const (
	ToolTransportNative  = "native"
	ToolTransportHTTP    = "http"
	ToolTransportMCPStdio = "mcp_stdio"
	ToolTransportMCPHTTP = "mcp_http"
)

// Tool approval policies.
const (
	ToolApprovalNever  = "never"
	ToolApprovalAlways = "always"
	ToolApprovalWrite  = "write"
	ToolApprovalNetwork = "network"
)

// Tool call statuses.
const (
	ToolCallPending   = "pending"
	ToolCallApproved  = "approved"
	ToolCallRunning   = "running"
	ToolCallCompleted = "completed"
	ToolCallFailed    = "failed"
	ToolCallRejected  = "rejected"
)

// ToolDefinition is one registered tool. Version is immutable: changing a
// schema or behaviour requires a new record with a higher version.
type ToolDefinition struct {
	ID             string   `json:"id"`
	OwnerID        string   `json:"owner_id,omitempty"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Transport      string   `json:"transport"`
	Description    string   `json:"description,omitempty"`
	InputSchema    string   `json:"input_schema,omitempty"`
	OutputSchema   string   `json:"output_schema,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	SideEffect     bool     `json:"side_effect"`
	TimeoutSec     int      `json:"timeout_sec,omitempty"`
	MaxOutputBytes int64    `json:"max_output_bytes,omitempty"`
	Approval       string   `json:"approval,omitempty"`
	Enabled        bool     `json:"enabled"`
	Builtin        bool     `json:"builtin,omitempty"`
	// Native is the implementation key for transport=native.
	Native string `json:"native,omitempty"`
	// HTTP is the outbound HTTP configuration for transport=http.
	HTTP *HTTPToolConfig `json:"http,omitempty"`
	// ServerID and RemoteName identify a tool discovered from an MCP server.
	ServerID   string `json:"server_id,omitempty"`
	RemoteName string `json:"remote_name,omitempty"`
	// Archived marks a superseded version. Disabled tools stay queryable so
	// historical ToolCallRecords still resolve their definition.
	Archived  bool      `json:"archived,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HTTPToolConfig describes an HTTP JSON tool endpoint. Header values are
// sealed at rest and never serialized.
type HTTPToolConfig struct {
	URL          string            `json:"url"`
	Method       string            `json:"method,omitempty"`
	HeaderKeys   []string          `json:"header_keys,omitempty"`
	HeaderSealed map[string]string `json:"header_sealed,omitempty"`
	AllowPrivateHosts bool       `json:"allow_private_hosts,omitempty"`
}

// MCPServer is a registered MCP server. Env/header values are AES-GCM sealed
// and never serialized; only the key names are exposed.
type MCPServer struct {
	ID           string            `json:"id"`
	OwnerID      string            `json:"owner_id,omitempty"`
	Name         string            `json:"name"`
	Transport    string            `json:"transport"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	EnvKeys      []string          `json:"env_keys,omitempty"`
	EnvSealed    map[string]string `json:"env_sealed,omitempty"`
	WorkingDir   string            `json:"working_dir,omitempty"`
	URL          string            `json:"url,omitempty"`
	HeaderKeys   []string          `json:"header_keys,omitempty"`
	HeaderSealed map[string]string `json:"header_sealed,omitempty"`
	AllowPrivateHosts bool       `json:"allow_private_hosts,omitempty"`
	TimeoutSec     int           `json:"timeout_sec,omitempty"`
	MaxOutputBytes int64         `json:"max_output_bytes,omitempty"`
	Fingerprint  string            `json:"fingerprint,omitempty"`
	Status       string            `json:"status,omitempty"`
	LastError    string            `json:"last_error,omitempty"`
	CreatedBy    string            `json:"created_by,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// ToolGrant authorizes one AgentProfile to call one tool. An empty Scopes list
// inherits the tool scopes; a non-empty list must cover every tool scope.
type ToolGrant struct {
	ID             string    `json:"id"`
	OwnerID        string    `json:"owner_id"`
	AgentProfileID string    `json:"agent_profile_id"`
	ToolID         string    `json:"tool_id"`
	Scopes         []string  `json:"scopes,omitempty"`
	Approval       string    `json:"approval,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ToolCallRecord is the durable audit record of one tool invocation.
type ToolCallRecord struct {
	ID            string         `json:"id"`
	PlanID        string         `json:"plan_id"`
	TaskID        string         `json:"task_id"`
	ToolID         string         `json:"tool_id"`
	ToolVersion    string         `json:"tool_version"`
	AgentProfileID string         `json:"agent_profile_id,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	ArgumentsHash string         `json:"arguments_hash"`
	ResultHash    string         `json:"result_hash,omitempty"`
	ResultPreview string         `json:"result_preview,omitempty"`
	Status        string         `json:"status"`
	ErrorCode     string         `json:"error_code,omitempty"`
	Error         string         `json:"error,omitempty"`
	ApprovalID    string         `json:"approval_id,omitempty"`
	DryRun        bool           `json:"dry_run,omitempty"`
	Truncated     bool           `json:"truncated,omitempty"`
	OutputBytes   int64          `json:"output_bytes,omitempty"`
	ReplayOf      string         `json:"replay_of,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	DurationMS    int64          `json:"duration_ms"`
}

// AgentProfileSnapshot freezes the profile/prompt/model versions used by a
// plan so a later edit cannot change what an old run meant.
type AgentProfileSnapshot struct {
	AgentProfileID  string `json:"agent_profile_id"`
	PromptVersionID string `json:"prompt_version_id,omitempty"`
	PromptVersion   int    `json:"prompt_version,omitempty"`
	PromptHash      string `json:"prompt_hash,omitempty"`
	ModelProfileID  string `json:"model_profile_id,omitempty"`
	ModelRevision   int    `json:"model_revision,omitempty"`
	Builtin         bool   `json:"builtin,omitempty"`
}

// NeedsApproval reports whether a tool call must pause for a human decision.
func (t ToolDefinition) NeedsApproval() bool {
	switch strings.ToLower(strings.TrimSpace(t.Approval)) {
	case ToolApprovalNever:
		return false
	case ToolApprovalAlways:
		return true
	case ToolApprovalWrite:
		for _, scope := range t.Scopes {
			switch strings.ToLower(strings.TrimSpace(scope)) {
			case "fs.write", "db", "shell":
				return true
			}
		}
		// A side-effecting tool with no explicit scope is still gated: the
		// safest default for an unknown side effect is human confirmation.
		return t.SideEffect && len(t.Scopes) == 0
	case ToolApprovalNetwork:
		for _, scope := range t.Scopes {
			if strings.EqualFold(strings.TrimSpace(scope), "net") {
				return true
			}
		}
		return t.SideEffect
	}
	// No explicit policy: side effects are gated, read-only tools are not.
	return t.SideEffect
}

// EffectiveApproval combines the tool policy with a grant policy, keeping the
// stricter of the two.
func (t ToolDefinition) EffectiveApproval(grant *ToolGrant) string {
	base := strings.ToLower(strings.TrimSpace(t.Approval))
	if base == "" {
		if t.SideEffect {
			base = ToolApprovalWrite
		} else {
			base = ToolApprovalNever
		}
	}
	if grant == nil {
		return base
	}
	override := strings.ToLower(strings.TrimSpace(grant.Approval))
	if override == "" {
		return base
	}
	rank := func(value string) int {
		switch value {
		case ToolApprovalNever:
			return 0
		case ToolApprovalWrite:
			return 1
		case ToolApprovalNetwork:
			return 1
		case ToolApprovalAlways:
			return 2
		default:
			return 1
		}
	}
	if rank(override) > rank(base) {
		return override
	}
	return base
}

// HashArguments returns a stable SHA-256 of a JSON object.
func HashArguments(arguments map[string]any) string {
	if arguments == nil {
		arguments = map[string]any{}
	}
	// encoding/json sorts map keys deterministically.
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashResult returns a stable SHA-256 of a tool result value.
func HashResult(value any) string {
	encoded, err := canonicalJSON(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalJSON(value any) ([]byte, error) {
	switch typed := value.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return typed, nil
	default:
		return json.Marshal(typed)
	}
}

// SortStrings returns a sorted copy without empty entries.
func SortStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}

// Package modelprofile stores per-user and global OpenAI-compatible model
// profiles, their routing and vision-capability probes.
package modelprofile

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProviderOpenAICompatible = "openai_compatible"

	VisionYes     = "yes"
	VisionNo      = "no"
	VisionUnknown = "unknown"

	RoleMain     = "main"
	RoleVision   = "vision"
	RoleFallback = "fallback"
)

var (
	// ErrNotFound is returned when a profile does not exist or is not visible
	// to the requesting owner.
	ErrNotFound = errors.New("model profile not found")
	// ErrForbidden is returned when a regular user mutates a global profile.
	ErrForbidden = errors.New("model profile forbidden")
	// ErrNameExists means the owner already has a profile with that name.
	ErrNameExists = errors.New("model profile name already exists")
	// ErrQuotaExceeded means the owner reached MODEL_PROFILE_MAX_PER_USER.
	ErrQuotaExceeded = errors.New("model profile quota exceeded")
	// ErrEncryptionUnavailable means MODEL_PROFILE_ENCRYPTION_KEY is missing;
	// saving a profile fail-closes instead of writing a plaintext key.
	ErrEncryptionUnavailable = errors.New("model profile encryption unavailable")
	// ErrInUse means a routing still references the profile.
	ErrInUse = errors.New("model profile is referenced by routing")
	// ErrInvalid marks caller input errors.
	ErrInvalid = errors.New("model profile invalid")
)

// Profile is one OpenAI-compatible model configuration. Secrets are never
// serialized: APIKeyCiphertext is AES-GCM output and the plaintext key lives in
// the unexported apiKey field only while the process holds it.
type Profile struct {
	ID                  string     `json:"id"`
	OwnerID             string     `json:"owner_id"`
	Name                string     `json:"name"`
	Provider            string     `json:"provider"`
	BaseURL             string     `json:"base_url"`
	Model               string     `json:"model"`
	APIKeyCiphertext    string     `json:"api_key_ciphertext,omitempty"`
	APIKeyLast4         string     `json:"api_key_last4,omitempty"`
	Roles               []string   `json:"roles,omitempty"`
	SupportsVision      string     `json:"supports_vision"`
	VisionProbeReason   string     `json:"vision_probe_reason,omitempty"`
	VisionProbedAt      *time.Time `json:"vision_probed_at,omitempty"`
	VisionOverriddenBy  string     `json:"vision_overridden_by,omitempty"`
	VisionOverriddenAt  *time.Time `json:"vision_overridden_at,omitempty"`
	TimeoutSeconds      int        `json:"timeout_seconds"`
	MaxOutputTokens     int        `json:"max_output_tokens,omitempty"`
	// ContextWindowTokens is the model context window used for prompt budgeting.
	// Zero means the 128000 default.
	ContextWindowTokens int        `json:"context_window_tokens,omitempty"`
	PromptPricePer1K    float64    `json:"prompt_price_per_1k,omitempty"`
	CompletionPricePer1K float64   `json:"completion_price_per_1k,omitempty"`
	Revision            int        `json:"revision"`
	Bootstrap           bool       `json:"bootstrap,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`

	apiKey string `json:"-"`
}

// Masked returns a copy safe to serialize to API clients.
func (p Profile) Masked() Profile {
	p.APIKeyCiphertext = ""
	p.apiKey = ""
	return p
}

// MaskedKey is the only representation of the secret a client ever sees.
func (p Profile) MaskedKey() string {
	if strings.TrimSpace(p.APIKeyLast4) == "" {
		return ""
	}
	return "****" + p.APIKeyLast4
}

// Input is the create/update payload. APIKey empty on update keeps the
// existing secret unchanged.
type Input struct {
	Name                 string
	Provider             string
	BaseURL              string
	Model                string
	APIKey               string
	Roles                []string
	SupportsVision       string
	TimeoutSeconds       int
	MaxOutputTokens      int
	ContextWindowTokens  int
	PromptPricePer1K     float64
	CompletionPricePer1K float64
}

// Routing binds the main text profile, ordered vision fallbacks and optional
// text fallbacks for one owner ("" means the global routing).
type Routing struct {
	OwnerID           string   `json:"owner_id"`
	MainProfileID     string   `json:"main_profile_id"`
	VisionProfileIDs  []string `json:"vision_profile_ids,omitempty"`
	FallbackProfileIDs []string `json:"fallback_profile_ids,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// Resolved is a usable profile plus the decrypted secret. APIKey is only kept
// in process memory and never logged or serialized.
type Resolved struct {
	Profile Profile
	APIKey  string
	Mode    string // main / vision_fallback / text_fallback / bootstrap
}

func normalizeRoles(roles []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		switch role {
		case RoleMain, RoleVision, RoleFallback:
		default:
			continue
		}
		if seen[role] {
			continue
		}
		seen[role] = true
		out = append(out, role)
	}
	return out
}

func normalizeVision(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VisionYes:
		return VisionYes
	case VisionNo:
		return VisionNo
	case VisionUnknown:
		return VisionUnknown
	default:
		return VisionUnknown
	}
}

func last4(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

func (p Profile) validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len([]rune(p.Name)) > 80 {
		return fmt.Errorf("%w: name is longer than 80 characters", ErrInvalid)
	}
	if p.Provider != ProviderOpenAICompatible {
		return fmt.Errorf("%w: unsupported provider %q", ErrInvalid, p.Provider)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("%w: base_url is required", ErrInvalid)
	}
	if strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalid)
	}
	if p.TimeoutSeconds <= 0 {
		p.TimeoutSeconds = 120
	}
	if p.ContextWindowTokens < 0 {
		return fmt.Errorf("%w: context_window_tokens must be positive", ErrInvalid)
	}
	if p.ContextWindowTokens == 0 {
		p.ContextWindowTokens = 128000
	}
	return nil
}

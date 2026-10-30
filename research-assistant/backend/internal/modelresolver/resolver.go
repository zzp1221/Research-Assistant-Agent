// Package modelresolver resolves the OpenAI-compatible model profiles a plan
// should use at runtime. Profiles are read from modelprofile.Store on every
// call behind a revision-keyed cache, so saving a profile takes effect for the
// next task without a restart (hot reload, design 4.3).
package modelresolver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
)

type ownerKey struct{}

// WithOwner attaches the plan owner to a task context so agents can resolve
// that owner's model profiles.
func WithOwner(ctx context.Context, ownerID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ownerKey{}, strings.TrimSpace(ownerID))
}

// OwnerFromContext returns the attached plan owner ("" for global).
func OwnerFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	owner, _ := ctx.Value(ownerKey{}).(string)
	return strings.TrimSpace(owner)
}

var (
	defaultMu       sync.RWMutex
	defaultResolver *Resolver
)

// SetDefault installs the process-wide resolver used by agents and the API.
func SetDefault(resolver *Resolver) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultResolver = resolver
}

// Default returns the process-wide resolver, or nil.
func Default() *Resolver {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultResolver
}

// Resolver resolves text/vision profiles for one owner. It is safe for
// concurrent use.
type Resolver struct {
	store *modelprofile.Store

	mu    sync.RWMutex
	cache map[string]*oaichat.Client
}

// New builds a resolver over store.
func New(store *modelprofile.Store) *Resolver {
	return &Resolver{store: store, cache: map[string]*oaichat.Client{}}
}

// Store exposes the backing profile store.
func (r *Resolver) Store() *modelprofile.Store {
	if r == nil {
		return nil
	}
	return r.store
}

// InvalidateAll drops cached clients; it is called after any profile or
// routing mutation.
func (r *Resolver) InvalidateAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.cache = map[string]*oaichat.Client{}
	r.mu.Unlock()
}

func (r *Resolver) clientFor(profile modelprofile.Profile) *oaichat.Client {
	key := fmt.Sprintf("%s@%d", profile.ID, profile.Revision)
	r.mu.RLock()
	client := r.cache[key]
	r.mu.RUnlock()
	if client != nil {
		return client
	}
	client = oaichat.NewClient(oaichat.AllowPrivateFromEnv())
	r.mu.Lock()
	r.cache[key] = client
	r.mu.Unlock()
	return client
}

// ResolveText returns the text profile for owner: explicit routing main, then
// the owner's first main-role profile, then the global routing/main, then the
// env bootstrap.
func (r *Resolver) ResolveText(ownerID string) (modelprofile.Resolved, error) {
	if r != nil && r.store != nil {
		if resolved, ok := r.resolveRouting(ownerID); ok {
			return resolved, nil
		}
	}
	return envBootstrap()
}

func (r *Resolver) resolveRouting(ownerID string) (modelprofile.Resolved, bool) {
	candidates := make([]string, 0, 2)
	if ownerID != "" {
		routing := r.store.GetRouting(ownerID)
		if routing.MainProfileID != "" {
			candidates = append(candidates, routing.MainProfileID)
		}
	}
	for _, profile := range r.store.List(ownerID) {
		if profile.OwnerID != ownerID || ownerID == "" {
			continue
		}
		if hasRole(profile.Roles, modelprofile.RoleMain) {
			candidates = append(candidates, profile.ID)
			break
		}
	}
	globalRouting := r.store.GetRouting("")
	if globalRouting.MainProfileID != "" {
		candidates = append(candidates, globalRouting.MainProfileID)
	}
	for _, profile := range r.store.List(ownerID) {
		if profile.OwnerID == "" && hasRole(profile.Roles, modelprofile.RoleMain) {
			candidates = append(candidates, profile.ID)
			break
		}
	}
	for _, id := range candidates {
		if resolved, ok := r.resolveProfile(id, ownerID, "main"); ok {
			return resolved, true
		}
	}
	return modelprofile.Resolved{}, false
}

// ResolveVision returns the ordered vision candidates: main when it can see
// images, then routing vision ids, then any owner/global vision profiles.
func (r *Resolver) ResolveVision(ownerID string) []modelprofile.Resolved {
	if r == nil || r.store == nil {
		return nil
	}
	out := make([]modelprofile.Resolved, 0, 4)
	seen := map[string]bool{}
	add := func(id, mode string) {
		if id == "" || seen[id] {
			return
		}
		resolved, ok := r.resolveProfile(id, ownerID, mode)
		if !ok {
			return
		}
		if resolved.Profile.SupportsVision == modelprofile.VisionNo {
			seen[id] = true
			return
		}
		seen[id] = true
		out = append(out, resolved)
	}
	// Design 4.5 step 2: use the main model when it is known to see images,
	// then the routing's ordered vision list. A profile with unknown capability
	// must not be assumed multimodal: doing so sends images to text-only models
	// and waits for the full vision timeout before falling back to inconclusive.
	// Users can run the probe endpoint (or set supports_vision=yes) to opt in.
	if main, err := r.ResolveText(ownerID); err == nil && main.Profile.SupportsVision == modelprofile.VisionYes {
		add(main.Profile.ID, "main")
	}
	routing := r.store.GetRouting(ownerID)
	for _, id := range routing.VisionProfileIDs {
		add(id, "vision_fallback")
	}
	return out
}

func (r *Resolver) resolveProfile(id, ownerID, mode string) (modelprofile.Resolved, bool) {
	profile, ok := r.store.GetRaw(id)
	if !ok {
		return modelprofile.Resolved{}, false
	}
	if profile.OwnerID != "" && profile.OwnerID != ownerID {
		return modelprofile.Resolved{}, false
	}
	secret, err := r.store.Secret(id)
	if err != nil && profile.ID != "env-bootstrap" {
		return modelprofile.Resolved{}, false
	}
	copy := *profile
	return modelprofile.Resolved{Profile: copy, APIKey: secret, Mode: mode}, true
}

func hasRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

// Snapshot records the profiles and revisions a plan resolves to now, so an
// in-flight plan can prove which model judged it even after a later edit.
func (r *Resolver) Snapshot(ownerID string) models.ModelSnapshot {
	snapshot := models.ModelSnapshot{}
	if r == nil {
		return snapshot
	}
	if text, err := r.ResolveText(ownerID); err == nil {
		snapshot.Text = &models.ProfileSnapshot{ProfileID: text.Profile.ID, Model: text.Profile.Model, Revision: text.Profile.Revision, Mode: text.Mode, ContextWindowTokens: text.Profile.ContextWindowTokens, MaxOutputTokens: text.Profile.MaxOutputTokens}
	}
	for _, vision := range r.ResolveVision(ownerID) {
		snapshot.Vision = append(snapshot.Vision, models.ProfileSnapshot{
			ProfileID: vision.Profile.ID,
			Model:     vision.Profile.Model,
			Revision:  vision.Profile.Revision,
			Mode:      vision.Mode,
		})
	}
	return snapshot
}

// Complete resolves the text profile and performs one chat completion.
func (r *Resolver) Complete(ctx context.Context, ownerID string, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error) {
	resolved, err := r.ResolveText(ownerID)
	if err != nil {
		return nil, modelprofile.Resolved{}, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout(resolved.Profile.TimeoutSeconds)
	}
	client := r.clientFor(resolved.Profile)
	response, err := client.Complete(ctx, resolved.Profile.BaseURL, resolved.APIKey, resolved.Profile.Model, messages, opts)
	return response, resolved, err
}

// CompleteVision performs one chat completion using an explicit profile.
func (r *Resolver) CompleteVision(ctx context.Context, resolved modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout(resolved.Profile.TimeoutSeconds)
	}
	client := r.clientFor(resolved.Profile)
	return client.Complete(ctx, resolved.Profile.BaseURL, resolved.APIKey, resolved.Profile.Model, messages, opts)
}

// ResolveProfile resolves one explicit model profile for an owner. It is used
// by AgentProfile execution so a profile can pin a model instead of inheriting
// the owner's main routing.
func (r *Resolver) ResolveProfile(ownerID, profileID string) (modelprofile.Resolved, error) {
	if r == nil {
		return modelprofile.Resolved{}, modelprofile.ErrNotFound
	}
	// "env-bootstrap" is the deployment's OPENAI_* default. It is a legitimate
	// profile target for custom agents so an installation without stored model
	// profiles can still run profile-bound tasks.
	if strings.EqualFold(strings.TrimSpace(profileID), "env-bootstrap") {
		return envBootstrap()
	}
	resolved, ok := r.resolveProfile(strings.TrimSpace(profileID), ownerID, "explicit")
	if !ok {
		return modelprofile.Resolved{}, modelprofile.ErrNotFound
	}
	return resolved, nil
}

// CompleteWithResolved performs one chat completion with an already resolved
// profile (AgentProfile model binding).
func (r *Resolver) CompleteWithResolved(ctx context.Context, resolved modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout(resolved.Profile.TimeoutSeconds)
	}
	client := r.clientFor(resolved.Profile)
	return client.Complete(ctx, resolved.Profile.BaseURL, resolved.APIKey, resolved.Profile.Model, messages, opts)
}

func defaultTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// envBootstrap builds an in-memory profile from OPENAI_* / llm config. It keeps
// deployments without a stored profile working exactly as before.
func envBootstrap() (modelprofile.Resolved, error) {
	cfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		return modelprofile.Resolved{}, err
	}
	profile := modelprofile.Profile{
		ID:                  "env-bootstrap",
		Name:                "环境默认模型",
		Provider:            modelprofile.ProviderOpenAICompatible,
		BaseURL:             cfg.BaseURL,
		Model:               cfg.Model,
		Roles:               []string{modelprofile.RoleMain, modelprofile.RoleVision},
		SupportsVision:      modelprofile.VisionUnknown,
		TimeoutSeconds:      120,
		ContextWindowTokens: 128000,
		Revision:            0,
		Bootstrap:           true,
	}
	return modelprofile.Resolved{Profile: profile, APIKey: cfg.APIKey, Mode: "bootstrap"}, nil
}

// DefaultStoreDir derives the on-disk profile directory from the environment.
func DefaultStoreDir() string {
	if dir := strings.TrimSpace(os.Getenv("MODEL_PROFILE_DIR")); dir != "" {
		return dir
	}
	if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
		return planPath + ".model-profiles"
	}
	return "./data/model-profiles"
}

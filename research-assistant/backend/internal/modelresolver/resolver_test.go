package modelresolver

import (
	"testing"

	"research-assistant-backend/internal/modelprofile"
)

const resolverTestKey = "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE="

func newTestStore(t *testing.T) *modelprofile.Store {
	t.Helper()
	t.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", resolverTestKey)
	store, err := modelprofile.NewStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return store
}

func createProfile(t *testing.T, store *modelprofile.Store, owner, name, model string, roles []string, vision string) modelprofile.Profile {
	t.Helper()
	profile, err := store.Create(owner, modelprofile.Input{
		Name:           name,
		BaseURL:        "https://" + name + ".example/v1",
		Model:          model,
		APIKey:         "sk-" + name,
		Roles:          roles,
		SupportsVision: vision,
	}, 20)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return profile
}

func TestResolveTextPrecedence(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	userA := createProfile(t, store, "a", "amain", "a-main", []string{modelprofile.RoleMain}, modelprofile.VisionUnknown)
	global := createProfile(t, store, "", "gmain", "g-main", []string{modelprofile.RoleMain}, modelprofile.VisionUnknown)

	// Without routing, the owner's own main role wins over the global main.
	resolved, err := resolver.ResolveText("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Profile.ID != userA.ID {
		t.Fatalf("resolve = %s, want owner profile %s", resolved.Profile.ID, userA.ID)
	}
	// Owner B has no profile and falls back to the global main.
	resolvedB, err := resolver.ResolveText("b")
	if err != nil {
		t.Fatalf("resolve b: %v", err)
	}
	if resolvedB.Profile.ID != global.ID {
		t.Fatalf("resolve b = %s, want global %s", resolvedB.Profile.ID, global.ID)
	}
	// Explicit routing overrides role defaults.
	other := createProfile(t, store, "a", "a-alt", "a-alt", []string{"vision"}, modelprofile.VisionUnknown)
	if _, err := store.SaveRouting("a", other.ID, nil, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	resolved, _ = resolver.ResolveText("a")
	if resolved.Profile.ID != other.ID {
		t.Fatalf("routing main did not win: %s", resolved.Profile.ID)
	}
}

func TestResolveVisionOrderingAndNoVisionExclusion(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	blind := createProfile(t, store, "a", "blind", "blind", []string{modelprofile.RoleMain}, modelprofile.VisionNo)
	vision1 := createProfile(t, store, "a", "v1", "v1", []string{modelprofile.RoleVision}, modelprofile.VisionYes)
	vision2 := createProfile(t, store, "a", "v2", "v2", []string{modelprofile.RoleVision}, modelprofile.VisionUnknown)
	if _, err := store.SaveRouting("a", blind.ID, []string{vision2.ID, vision1.ID}, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	candidates := resolver.ResolveVision("a")
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[0].Profile.ID != vision2.ID || candidates[0].Mode != "vision_fallback" {
		t.Fatalf("routing order not respected: %#v", candidates)
	}
	if candidates[1].Profile.ID != vision1.ID {
		t.Fatalf("second candidate = %s", candidates[1].Profile.ID)
	}
	for _, candidate := range candidates {
		if candidate.Profile.ID == blind.ID {
			t.Fatalf("a profile with supports_vision=no must not judge images")
		}
	}
}

func TestResolveVisionSkipsUnknownMainProfile(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	main := createProfile(t, store, "a", "unknown-main", "m", []string{modelprofile.RoleMain}, modelprofile.VisionUnknown)
	if _, err := store.SaveRouting("a", main.ID, nil, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if candidates := resolver.ResolveVision("a"); len(candidates) != 0 {
		t.Fatalf("an unknown-capability main profile must not be assumed multimodal: %#v", candidates)
	}
}

func TestResolveVisionDoesNotPickUnroutedProfiles(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	main := createProfile(t, store, "a", "main", "m", []string{modelprofile.RoleMain}, modelprofile.VisionNo)
	if _, err := store.SaveRouting("a", main.ID, nil, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	// A vision-capable profile exists but is not routed to this owner.
	createProfile(t, store, "a", "spare-vision", "v", []string{modelprofile.RoleVision}, modelprofile.VisionYes)
	if candidates := resolver.ResolveVision("a"); len(candidates) != 0 {
		t.Fatalf("unrouted vision profiles must not be used: %#v", candidates)
	}
}

func TestSnapshotRecordsRevisions(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	main := createProfile(t, store, "a", "main", "m", []string{modelprofile.RoleMain}, modelprofile.VisionYes)
	if _, err := store.SaveRouting("a", main.ID, []string{main.ID}, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	snapshot := resolver.Snapshot("a")
	if snapshot.Text == nil || snapshot.Text.ProfileID != main.ID || snapshot.Text.Revision != 1 {
		t.Fatalf("text snapshot = %#v", snapshot.Text)
	}
	if len(snapshot.Vision) == 0 || snapshot.Vision[0].ProfileID != main.ID {
		t.Fatalf("vision snapshot = %#v", snapshot.Vision)
	}
	// A profile edit changes the revision, so the next snapshot differs.
	if _, err := store.Update("a", main.ID, modelprofile.Input{APIKey: "rotated-key-1"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated := resolver.Snapshot("a")
	if updated.Text == nil || updated.Text.Revision != 2 {
		t.Fatalf("revision after edit = %#v", updated.Text)
	}
}

func TestHotReloadInvalidatesCache(t *testing.T) {
	store := newTestStore(t)
	resolver := New(store)
	main := createProfile(t, store, "a", "main", "m1", []string{modelprofile.RoleMain}, modelprofile.VisionUnknown)
	first, err := resolver.ResolveText("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if first.Profile.Model != "m1" {
		t.Fatalf("first model = %s", first.Profile.Model)
	}
	if _, err := store.Update("a", main.ID, modelprofile.Input{Model: "m2"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	resolver.InvalidateAll()
	second, err := resolver.ResolveText("a")
	if err != nil {
		t.Fatalf("resolve after edit: %v", err)
	}
	if second.Profile.Model != "m2" || second.Profile.Revision != 2 {
		t.Fatalf("hot reload failed: %#v", second.Profile)
	}
}

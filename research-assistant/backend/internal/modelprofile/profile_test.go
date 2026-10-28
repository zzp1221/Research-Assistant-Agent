package modelprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testEncryptionKey = "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE="

func withEncryptionKey(t *testing.T) {
	t.Helper()
	previous, had := os.LookupEnv(EncryptionKeyEnv)
	if err := os.Setenv(EncryptionKeyEnv, testEncryptionKey); err != nil {
		t.Fatalf("set env: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(EncryptionKeyEnv, previous)
			return
		}
		_ = os.Unsetenv(EncryptionKeyEnv)
	})
}

func TestEncryptDecryptRoundTripAndAADBinding(t *testing.T) {
	withEncryptionKey(t)
	ciphertext, err := encryptSecret("sk-super-secret", "profile-1", "owner-1", 1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(ciphertext, "sk-super-secret") {
		t.Fatalf("ciphertext leaks plaintext")
	}
	plain, err := decryptSecret(ciphertext, "profile-1", "owner-1", 1)
	if err != nil || plain != "sk-super-secret" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := decryptSecret(ciphertext, "profile-2", "owner-1", 1); err == nil {
		t.Fatalf("decrypt must fail when AAD profile id changes")
	}
	if _, err := decryptSecret(ciphertext, "profile-1", "owner-1", 2); err == nil {
		t.Fatalf("decrypt must fail when AAD revision changes")
	}
}

func TestEncryptionFailClosedWithoutKey(t *testing.T) {
	_ = os.Unsetenv(EncryptionKeyEnv)
	if EncryptionAvailable() {
		t.Fatalf("encryption must be unavailable without a key")
	}
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	_, createErr := store.Create("owner", Input{Name: "p", BaseURL: "https://example.com/v1", Model: "m", APIKey: "sk"}, 10)
	if createErr == nil || !strings.Contains(createErr.Error(), ErrEncryptionUnavailable.Error()) {
		t.Fatalf("create must fail closed, got %v", createErr)
	}
}

func TestStorePersistsMaskedSecretAndNeverSerializesIt(t *testing.T) {
	withEncryptionKey(t)
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	created, err := store.Create("owner-a", Input{Name: "Main", BaseURL: "https://example.com/v1", Model: "m1", APIKey: "sk-abcdefgh1234", Roles: []string{"main"}}, 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.APIKeyLast4 != "1234" || created.MaskedKey() != "****1234" {
		t.Fatalf("mask = %q / %q", created.APIKeyLast4, created.MaskedKey())
	}
	raw, err := os.ReadFile(filepath.Join(dir, profilesFileName))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Contains(string(raw), "sk-abcdefgh1234") {
		t.Fatalf("plaintext key was written to disk")
	}
	encoded, _ := json.Marshal(created)
	if strings.Contains(string(encoded), "sk-abcdefgh1234") || strings.Contains(string(encoded), "ciphertext") {
		t.Fatalf("serialized profile leaks a secret: %s", encoded)
	}
	secret, err := store.Secret(created.ID)
	if err != nil || secret != "sk-abcdefgh1234" {
		t.Fatalf("secret = %q, %v", secret, err)
	}
}

func TestRoutingAndVisibilityMatrix(t *testing.T) {
	withEncryptionKey(t)
	store, _ := NewStore("")
	userA, err := store.Create("a", Input{Name: "A main", BaseURL: "https://a.example/v1", Model: "am", APIKey: "key-a", Roles: []string{"main"}}, 10)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	userB, _ := store.Create("b", Input{Name: "B main", BaseURL: "https://b.example/v1", Model: "bm", APIKey: "key-b", Roles: []string{"main"}}, 10)
	if _, err := store.Create("b", Input{Name: "B vision", BaseURL: "https://b.example/v1", Model: "bv", APIKey: "key-bv", Roles: []string{"vision"}}, 10); err != nil {
		t.Fatalf("create b vision: %v", err)
	}
	if _, ok := store.Get("a", userB.ID); ok {
		t.Fatalf("owner A must not see owner B profile")
	}
	if len(store.List("a")) != 1 {
		t.Fatalf("owner A must see exactly own profile")
	}
	if _, err := store.SaveRouting("a", userB.ID, nil, nil); err == nil {
		t.Fatalf("routing must reject a profile invisible to the owner")
	}
	if _, err := store.SaveRouting("a", userA.ID, nil, nil); err != nil {
		t.Fatalf("save routing: %v", err)
	}
	routing := store.GetRouting("a")
	if routing.MainProfileID != userA.ID {
		t.Fatalf("routing main = %q", routing.MainProfileID)
	}
	if err := store.Delete("a", userA.ID); err == nil {
		t.Fatalf("delete must be blocked while routing references the profile")
	}
	if _, err := store.SaveRouting("a", "", nil, nil); err != nil {
		t.Fatalf("clear routing: %v", err)
	}
	if err := store.Delete("a", userA.ID); err != nil {
		t.Fatalf("delete after clearing routing: %v", err)
	}
}

func TestNameUniquenessAndQuota(t *testing.T) {
	withEncryptionKey(t)
	store, _ := NewStore("")
	_, err := store.Create("a", Input{Name: "same", BaseURL: "https://x.example/v1", Model: "m", APIKey: "k1"}, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Create("a", Input{Name: "SAME", BaseURL: "https://x.example/v1", Model: "m", APIKey: "k2"}, 2); err == nil {
		t.Fatalf("duplicate name must be rejected")
	}
	if _, err := store.Create("a", Input{Name: "second", BaseURL: "https://x.example/v1", Model: "m", APIKey: "k3"}, 2); err != nil {
		t.Fatalf("second profile: %v", err)
	}
	if _, err := store.Create("a", Input{Name: "third", BaseURL: "https://x.example/v1", Model: "m", APIKey: "k4"}, 2); err == nil {
		t.Fatalf("quota must be enforced")
	}
}

func TestUpdateBumpsRevisionAndKeepsSecret(t *testing.T) {
	withEncryptionKey(t)
	store, _ := NewStore("")
	created, _ := store.Create("a", Input{Name: "n", BaseURL: "https://x.example/v1", Model: "m", APIKey: "first-key"}, 10)
	updated, err := store.Update("a", created.ID, Input{Name: "n2"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Revision != created.Revision+1 {
		t.Fatalf("every update must bump revision: %d -> %d", created.Revision, updated.Revision)
	}
	if secret, err := store.Secret(created.ID); err != nil || secret != "first-key" {
		t.Fatalf("metadata update must keep the secret, got %q %v", secret, err)
	}
	withKey, err := store.Update("a", created.ID, Input{APIKey: "second-key"})
	if err != nil {
		t.Fatalf("update key: %v", err)
	}
	if withKey.Revision != created.Revision+2 {
		t.Fatalf("key update must bump revision")
	}
	secret, _ := store.Secret(created.ID)
	if secret != "second-key" {
		t.Fatalf("secret = %q", secret)
	}
	// last4 must be the final four characters of the new key.
	if withKey.APIKeyLast4 != "t-key"[1:] {
		t.Fatalf("last4 = %q", withKey.APIKeyLast4)
	}
}

func TestBootstrapRotatesAPIKey(t *testing.T) {
	withEncryptionKey(t)
	store, _ := NewStore("")
	if _, err := store.UpsertBootstrap("https://env.example/v1", "env-model", "first-key"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var before Profile
	for _, profile := range store.List("a") {
		if profile.Bootstrap {
			before = profile
		}
	}
	if before.ID == "" {
		t.Fatal("bootstrap profile not found")
	}
	if _, err := store.UpsertBootstrap("https://env.example/v1", "env-model", "second-key"); err != nil {
		t.Fatalf("bootstrap rotate: %v", err)
	}
	var after Profile
	for _, profile := range store.List("a") {
		if profile.Bootstrap {
			after = profile
		}
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("bootstrap key rotation must bump revision: before=%d after=%d", before.Revision, after.Revision)
	}
	secret, err := store.Secret(after.ID)
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if secret != "second-key" {
		t.Fatalf("bootstrap secret = %q, want second-key", secret)
	}
}

func TestBootstrapIsIdempotentAndDoesNotOverrideUserProfiles(t *testing.T) {
	withEncryptionKey(t)
	store, _ := NewStore("")
	if _, err := store.Create("a", Input{Name: "mine", BaseURL: "https://mine.example/v1", Model: "mine", APIKey: "mine-key"}, 10); err != nil {
		t.Fatalf("user profile: %v", err)
	}
	if _, err := store.UpsertBootstrap("https://env.example/v1", "env-model", "env-key"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := store.UpsertBootstrap("https://env.example/v1", "env-model", "env-key"); err != nil {
		t.Fatalf("bootstrap twice: %v", err)
	}
	profiles := store.List("a")
	globals := 0
	for _, profile := range profiles {
		if profile.OwnerID == "" {
			globals++
		}
		if profile.OwnerID == "a" && profile.Model != "mine" {
			t.Fatalf("bootstrap overrode user profile: %#v", profile)
		}
	}
	if globals != 1 {
		t.Fatalf("global profiles = %d, want 1", globals)
	}
}

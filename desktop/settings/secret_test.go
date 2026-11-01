package settings

import "testing"

func TestMemorySecretStore(t *testing.T) {
	store := NewMemorySecretStore()
	if err := store.Save("default", []byte("sk-test")); err != nil {
		t.Fatal(err)
	}
	value, err := store.Load("default")
	if err != nil || string(value) != "sk-test" {
		t.Fatalf("Load = %q, %v", value, err)
	}
	if err := store.Delete("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("default"); err == nil {
		t.Fatalf("expected missing secret after delete")
	}
}

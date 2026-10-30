package project

import (
	"path/filepath"
	"testing"
)

func TestMemoryStoreOwnerIsolation(t *testing.T) {
	store := NewMemoryStore()
	created, err := store.Create("owner-a", "Paper A", "desc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("owner-b", created.ID); err != ErrNotFound {
		t.Fatalf("foreign get err=%v", err)
	}
	if _, err := store.Update("owner-b", created.ID, Patch{Archived: boolPtr(true)}); err != ErrNotFound {
		t.Fatalf("foreign update err=%v", err)
	}
	if err := store.Delete("owner-b", created.ID); err != ErrNotFound {
		t.Fatalf("foreign delete err=%v", err)
	}
	if list, _ := store.List("owner-b", true); len(list) != 0 {
		t.Fatalf("foreign list=%#v", list)
	}
}

func TestMemoryStoreValidation(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Create("owner-a", " ", ""); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := store.Create("owner-a", "Same", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("owner-a", "same", ""); err == nil {
		t.Fatal("duplicate name must fail")
	}
	first, _ := store.Create("owner-a", "First", "")
	second, _ := store.Create("owner-a", "Second", "")
	if _, err := store.Update("owner-a", second.ID, Patch{Name: strPtr("First")}); err == nil {
		t.Fatal("rename to duplicate must fail")
	}
	archived := true
	updated, err := store.Update("owner-a", first.ID, Patch{Archived: &archived})
	if err != nil || !updated.Archived {
		t.Fatalf("archive failed: %#v %v", updated, err)
	}
	if list, _ := store.List("owner-a", false); len(list) != 2 {
		t.Fatalf("archived project leaked into default list: %#v", list)
	}
	if list, _ := store.List("owner-a", true); len(list) != 3 {
		t.Fatalf("include archived list=%d", len(list))
	}
}

func TestFileStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create("owner-a", "Persisted", "d")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("owner-a", created.ID)
	if err != nil || got.Name != "Persisted" {
		t.Fatalf("reloaded=%#v err=%v", got, err)
	}
}

func boolPtr(value bool) *bool    { return &value }
func strPtr(value string) *string { return &value }

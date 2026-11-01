package session

import (
	"context"
	"testing"
)

func TestSessionStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir + "/sessions.json")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), "u1", "p1", "研究", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Kind != "chat" {
		t.Fatalf("unexpected session: %#v", created)
	}
	ensured, err := store.Ensure(context.Background(), created.ID, "u1", "p1", "chat")
	if err != nil || ensured.ID != created.ID {
		t.Fatalf("ensure failed: %v %#v", err, ensured)
	}
	if _, err := store.Get(context.Background(), "u2", created.ID); err == nil {
		t.Fatal("cross-owner get must fail")
	}
	list, err := store.List(context.Background(), "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list failed: %v %d", err, len(list))
	}
	if err := store.Delete(context.Background(), "u1", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "u1", created.ID); err == nil {
		t.Fatal("deleted session must be gone")
	}
}

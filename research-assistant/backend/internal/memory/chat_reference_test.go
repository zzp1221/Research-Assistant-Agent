package memory

import (
	"testing"
	"time"
)

// referenceTurn builds a user turn carrying document provenance.
func referenceTurn(content string) ChatTurn {
	return ChatTurn{
		Role: "user", Content: content, CreatedAt: time.Now().UTC(),
		References: []ChatReference{{
			UploadID: "11111111-1111-4111-8111-111111111111", DocumentName: "paper.pdf",
			Page: 2, Quote: "selected sentence", Source: "user_selection",
		}},
	}
}

// TestChatStoreClonesReferenceSlices pins the isolation promise: mutating a
// returned session, or a caller's turn, must never rewrite persisted
// provenance or a sibling copy.
func TestChatStoreClonesReferenceSlices(t *testing.T) {
	store, err := NewChatStore(t.TempDir() + "/chat.json")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "s-clone"
	turn := referenceTurn("第二页说了什么？")
	appended, err := store.Append("u1", sessionID, "p1", turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(appended.Turns) != 1 || len(appended.Turns[0].References) != 1 {
		t.Fatalf("append did not keep the reference: %#v", appended.Turns)
	}

	// A caller mutating its own turn after Append must not reach the store.
	turn.References[0].Quote = "mutated by caller"
	turn.References[0].Page = 99
	stored, ok := store.Get("u1", sessionID)
	if !ok {
		t.Fatal("session was not persisted")
	}
	if stored.Turns[0].References[0].Quote != "selected sentence" || stored.Turns[0].References[0].Page != 2 {
		t.Fatalf("caller mutation leaked into the store: %#v", stored.Turns[0].References)
	}

	// Mutating a returned copy must not reach the store either.
	stored.Turns[0].References[0].Quote = "mutated by getter"
	stored.Turns[0].References = append(stored.Turns[0].References, ChatReference{Page: 7})
	again, _ := store.Get("u1", sessionID)
	if len(again.Turns[0].References) != 1 || again.Turns[0].References[0].Quote != "selected sentence" {
		t.Fatalf("returned copy aliased the store: %#v", again.Turns[0].References)
	}

	// List must return independent slices too.
	listed := store.List("u1")
	if len(listed) != 1 {
		t.Fatalf("expected one listed session, got %d", len(listed))
	}
	listed[0].Turns[0].References[0].DocumentName = "hijacked.pdf"
	final, _ := store.Get("u1", sessionID)
	if final.Turns[0].References[0].DocumentName != "paper.pdf" {
		t.Fatalf("List aliased the store: %#v", final.Turns[0].References)
	}

	// The provenance still survives a reopen.
	reopened, err := NewChatStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := reopened.Get("u1", sessionID)
	if !ok || len(reloaded.Turns[0].References) != 1 {
		t.Fatalf("reference did not persist across reopen: %#v", reloaded.Turns)
	}
	if ref := reloaded.Turns[0].References[0]; ref.Quote != "selected sentence" || ref.Source != "user_selection" || ref.Page != 2 {
		t.Fatalf("unexpected persisted reference: %#v", ref)
	}
}

// TestChatStoreWorkingStateClonesReferences covers the handoff path, which
// copies turn structs directly instead of going through cloneSession.
func TestChatStoreWorkingStateClonesReferences(t *testing.T) {
	store, err := NewChatStore(t.TempDir() + "/chat.json")
	if err != nil {
		t.Fatal(err)
	}
	turn := referenceTurn("实测结果：基线 NDCG 0.42")
	turn.References = append(turn.References, ChatReference{
		UploadID: "22222222-2222-4222-8222-222222222222", Page: 4, Quote: "baseline table",
	})
	if _, err := store.Append("u1", "s-state", "p1", turn); err != nil {
		t.Fatal(err)
	}
	state := store.WorkingState("u1", "s-state")
	if len(state) == 0 {
		t.Fatalf("expected a research state entry, got %#v", state)
	}
	for _, working := range state {
		for i := range working.References {
			working.References[i].Quote = "hijacked"
		}
	}
	stored, _ := store.Get("u1", "s-state")
	if stored.Turns[0].References[0].Quote != "selected sentence" {
		t.Fatalf("WorkingState aliased the store: %#v", stored.Turns[0].References)
	}
}

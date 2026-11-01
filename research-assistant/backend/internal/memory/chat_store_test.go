package memory

import (
	"strings"
	"testing"
	"time"
)

func TestChatStorePersistsAndCompacts(t *testing.T) {
	dir := t.TempDir()
	store, err := NewChatStore(dir + "/chat.json")
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Create("u1", "p1", "长对话")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		content := strings.Repeat("这是第"+string(rune('A'+i%26))+"轮的用户问题与上下文。", 60)
		if _, err := store.Append("u1", session.ID, "p1", ChatTurn{Role: "user", Content: content, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Append("u1", session.ID, "p1", ChatTurn{Role: "assistant", Content: content, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	summary, turns := store.ContextTurns("u1", session.ID, 5000, 10)
	if summary == "" {
		t.Fatal("expected summary for dropped turns")
	}
	if len(turns) == 0 || len(turns) > 10 {
		t.Fatalf("expected <=10 recent turns, got %d", len(turns))
	}
	total := 0
	for _, turn := range turns {
		total += turn.Tokens
	}
	if total > 5000 {
		t.Fatalf("recent turns exceed budget: %d", total)
	}
	reopened, err := NewChatStore(dir + "/chat.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get("u1", session.ID); !ok {
		t.Fatal("chat session did not persist")
	}
	if _, ok := reopened.Get("u2", session.ID); ok {
		t.Fatal("cross-owner chat session must not be visible")
	}
}

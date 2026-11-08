package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"research-assistant-backend/internal/agent"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

type captureChatModel struct {
	mu    sync.Mutex
	calls [][]*schema.Message
}

func (m *captureChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*schema.Message, len(input))
	for i, message := range input {
		copied[i] = &schema.Message{Role: message.Role, Content: message.Content}
	}
	m.calls = append(m.calls, copied)
	return &schema.Message{Content: "好的，我记住了。"}, nil
}

func (m *captureChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not implemented")
}

func TestChatMultiTurnPersistsHistoryAndInjectsCoreMemory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	t.Setenv("MEMORY_ENABLED", "true")
	memoryRuntimeOnce.Do(func() {})
	sessionRuntimeOnce.Do(func() {})
	memoryRuntimeOnce.enabled = true
	memoryRuntimeOnce.store = memory.NewMemoryStore()
	memoryRuntimeOnce.settings, _ = memory.NewSettingsStore("")
	sessionRuntimeOnce.sessions, _ = session.NewFileStore(t.TempDir() + "/sessions.json")
	sessionRuntimeOnce.chats, _ = memory.NewChatStore(t.TempDir() + "/chat.json")

	profile := memory.Record{
		Kind: memory.KindProfile, Scope: memory.Scope{OwnerID: "u1"},
		Title: "用户偏好", Content: "用户喜欢蓝色", Pinned: true, Importance: 10,
		Trust: memory.TrustUserExplicit, Status: memory.StatusActive, Source: memory.Source{Type: "user"},
	}
	if _, err := memoryRuntimeOnce.store.Put(context.Background(), profile); err != nil {
		t.Fatal(err)
	}

	fake := &captureChatModel{}
	chatAgent := &agent.ChatAgent{Name: "chat_agent", SystemPrompt: "system", Model: fake}
	router := gin.New()
	router.POST("/api/chat", func(c *gin.Context) { handleChat(c, chatAgent) })

	send := func(message string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"message": message, "session_id": "s-chat-test"})
		request := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(userIDHeaderName, "u1")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var decoded map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return response.Code, decoded
	}

	if code, decoded := send("我叫小明，我喜欢蓝色"); code != http.StatusOK || decoded["session_id"] != "s-chat-test" {
		t.Fatalf("turn1 code=%d body=%#v", code, decoded)
	}
	if code, decoded := send("我喜欢什么颜色？"); code != http.StatusOK {
		t.Fatalf("turn2 code=%d body=%#v", code, decoded)
	} else if citations, ok := decoded["citations"].([]any); !ok || len(citations) == 0 {
		t.Fatalf("expected citations, got %#v", decoded["citations"])
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(fake.calls))
	}
	second := fake.calls[1]
	joined := strings.Builder{}
	for _, message := range second {
		joined.WriteString(message.Content)
		joined.WriteString("\n")
	}
	text := joined.String()
	if !strings.Contains(text, "我叫小明") {
		t.Fatalf("second call lost the first user turn: %s", text)
	}
	if !strings.Contains(text, "好的，我记住了") {
		t.Fatalf("second call lost the first assistant turn: %s", text)
	}
	if !strings.Contains(text, "[core memory]") || !strings.Contains(text, "用户喜欢蓝色") {
		t.Fatalf("second call missing core memory: %s", text)
	}
}

func TestChatPreservesMoreThanTwentyTurns(t *testing.T) {
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	memoryRuntimeOnce.Do(func() {})
	sessionRuntimeOnce.Do(func() {})
	sessionRuntimeOnce.sessions, _ = session.NewFileStore(t.TempDir() + "/sessions.json")
	sessionRuntimeOnce.chats, _ = memory.NewChatStore(t.TempDir() + "/chat.json")
	for i := 0; i < 24; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		_, err := sessionRuntimeOnce.chats.Append("history-owner", "long-history", "", memory.ChatTurn{Role: role, Content: fmt.Sprintf("evidence-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	fake := &captureChatModel{}
	router := gin.New()
	router.POST("/api/chat", func(c *gin.Context) { handleChat(c, &agent.ChatAgent{Model: fake}) })
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"continue","session_id":"long-history","use_memory":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, "history-owner")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("chat failed %s", response.Body)
	}
	joined := ""
	for _, m := range fake.calls[0] {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "evidence-0\n") || !strings.Contains(joined, "evidence-23\n") {
		t.Fatal("early history was dropped")
	}
}

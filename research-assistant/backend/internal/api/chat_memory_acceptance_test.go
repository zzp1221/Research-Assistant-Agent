package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/agent"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

type evidenceChatModel struct {
	calls           [][]*schema.Message
	answer          string
	extractionCalls int
}

func (m *evidenceChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls = append(m.calls, input)
	if strings.Contains(input[0].Content, "Extract durable research facts") {
		m.extractionCalls++
		return &schema.Message{Content: `{"quotes":["数据版本 corpus-v7。","hallucinated fact"]}`}, nil
	}
	return &schema.Message{Content: m.answer}, nil
}
func (m *evidenceChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unused")
}

func TestAcceptanceChatMemorySwitchesAndEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	t.Setenv("MEMORY_ENABLED", "true")
	t.Setenv("MEMORY_MODE", "on")
	memoryRuntimeOnce.Do(func() {})
	sessionRuntimeOnce.Do(func() {})
	wikiRuntimeOnce.Do(func() {})
	oldStore, oldSettings, oldEnabled := memoryRuntimeOnce.store, memoryRuntimeOnce.settings, memoryRuntimeOnce.enabled
	oldSessions, oldChats := sessionRuntimeOnce.sessions, sessionRuntimeOnce.chats
	oldWikiEnabled, oldBuilder := wikiRuntimeOnce.enabled, wikiRuntimeOnce.builder
	t.Cleanup(func() {
		memoryRuntimeOnce.store, memoryRuntimeOnce.settings, memoryRuntimeOnce.enabled = oldStore, oldSettings, oldEnabled
		sessionRuntimeOnce.sessions, sessionRuntimeOnce.chats = oldSessions, oldChats
		wikiRuntimeOnce.enabled, wikiRuntimeOnce.builder = oldWikiEnabled, oldBuilder
	})
	s := memory.NewMemoryStore()
	settings, _ := memory.NewSettingsStore("")
	memoryRuntimeOnce.store, memoryRuntimeOnce.settings, memoryRuntimeOnce.enabled = s, settings, true
	sessionRuntimeOnce.sessions, _ = session.NewFileStore(t.TempDir() + "/sessions.json")
	sessionRuntimeOnce.chats, _ = memory.NewChatStore(t.TempDir() + "/chat.json")
	wikiRuntimeOnce.enabled = false
	rec, err := s.Put(context.Background(), memory.Record{Kind: memory.KindFact, Scope: memory.Scope{OwnerID: "o", ProjectID: "rag"}, Title: "划分约束", Content: "固定划分为 SPLIT-73F。", Trust: memory.TrustUserExplicit, Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	fake := &evidenceChatModel{answer: "记录是 [[memory:" + rec.ID + "|固定划分为 SPLIT-73F。]]"}
	router := gin.New()
	router.POST("/api/chat", func(c *gin.Context) { handleChat(c, &agent.ChatAgent{Model: fake}) })
	RegisterMemoryRoutes(router.Group("/api"), s, settings)
	send := func(method, path string, body any, owner string) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(userIDHeaderName, owner)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var got map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		return w.Code, got
	}
	chat := func(id string, use bool) (int, map[string]any) {
		return send("POST", "/api/chat", gin.H{"message": "划分约束是什么？", "session_id": id, "project_id": "rag", "use_memory": use}, "o")
	}
	code, got := chat("on", true)
	if code != 200 || len(got["answer_citations"].([]any)) != 1 || len(got["retrieved_sources"].([]any)) != 1 {
		t.Fatal("missing verified excerpt", code, got)
	}
	fake.answer = "没有划分信息 [[memory:" + rec.ID + "|GPU budget unrelated]] [[memory:unknown|固定划分为 SPLIT-73F。]]"
	_, got = chat("unsupported", true)
	if got["answer_citations"] != nil {
		t.Fatal("candidate became answer evidence", got)
	}
	fake.answer = "不知道。其他记录有 [[memory:" + rec.ID + "|固定划分为 SPLIT-73F。]]"
	_, got = chat("unknown", true)
	if got["answer_citations"] != nil {
		t.Fatal("unknown answer acquired unrelated evidence", got)
	}
	for _, mode := range []string{"off", "shadow", "on"} {
		v := memory.DefaultSettings()
		v.Mode = mode
		_ = settings.Put("o", v)
		use := mode != "on"
		_, got = chat("switch-"+mode, use)
		last := fake.calls[len(fake.calls)-1]
		for _, m := range last {
			if strings.Contains(m.Content, "SPLIT-73F") {
				t.Fatal("switch bypass", mode)
			}
		}
		if got["retrieved_sources"] != nil || got["answer_citations"] != nil {
			t.Fatal("disabled sources exposed", mode, got)
		}
	}
	v := memory.DefaultSettings()
	_ = settings.Put("o", v)
	// The server restores the recorded project when the client omits it.
	code, _ = send("POST", "/api/chat", gin.H{"message": "继续", "session_id": "on"}, "o")
	if code != 200 {
		t.Fatal("session project not restored")
	}
	code, _ = send("POST", "/api/chat", gin.H{"message": "继续", "session_id": "on", "project_id": "other"}, "o")
	if code == 200 {
		t.Fatal("project changed")
	}
	code, _ = send("POST", "/api/chat", gin.H{"message": "继续", "session_id": "on", "project_id": "rag"}, "other")
	if code == 200 {
		t.Fatal("owner changed")
	}
	code, _ = send("POST", "/api/memories/search", gin.H{"query": "划分", "as_of": "invalid"}, "o")
	if code != http.StatusBadRequest {
		t.Fatal("invalid as_of accepted")
	}
	fake.answer = "已核对。"
	_, _ = send("POST", "/api/chat", gin.H{"message": "数据版本 corpus-v7。", "session_id": "extract-off", "project_id": "rag"}, "o")
	if fake.extractionCalls != 0 {
		t.Fatal("disabled LLM extraction ran")
	}
	v.LLMExtraction = true
	_ = settings.Put("o", v)
	_, _ = send("POST", "/api/chat", gin.H{"message": "数据版本 corpus-v7。", "session_id": "extract-on", "project_id": "rag"}, "o")
	if fake.extractionCalls != 1 {
		t.Fatal("LLM setting was not consumed")
	}
	all, _, _ := s.List(context.Background(), memory.Query{OwnerID: "o", Statuses: []memory.Status{memory.StatusPending}})
	if len(all) != 1 || all[0].Content != "数据版本 corpus-v7。" || len(all[0].Refs) != 1 {
		t.Fatal("unverified LLM extraction became trusted", all)
	}
}

func TestAcceptanceZeroRetrievalBudget(t *testing.T) {
	s := memory.NewMemoryStore()
	_, _ = s.Put(context.Background(), memory.Record{Kind: memory.KindFact, Scope: memory.Scope{OwnerID: "o"}, Title: "needle", Content: "needle value"})
	got, _ := s.Retrieve(context.Background(), memory.Query{OwnerID: "o", Text: "needle"}, 0, 0)
	if got.Tokens != 0 || got.RetrievedText != "" {
		t.Fatal("zero budget reset to default")
	}
}

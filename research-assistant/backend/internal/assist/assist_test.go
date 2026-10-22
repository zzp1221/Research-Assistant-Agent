package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"research-assistant-backend/internal/appconfig"
)

func TestChatCompletionsURLHandlesVersionedAndBareBases(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com":              "https://api.deepseek.com/chat/completions",
		"https://api.deepseek.com/":             "https://api.deepseek.com/chat/completions",
		"https://api.deepseek.com/v1":           "https://api.deepseek.com/v1/chat/completions",
		"https://api.deepseek.com/v1/":          "https://api.deepseek.com/v1/chat/completions",
		"https://example.test/chat/completions": "https://example.test/chat/completions",
	}
	for input, expected := range cases {
		if got := chatCompletionsURL(input); got != expected {
			t.Fatalf("chatCompletionsURL(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestExtractJSONObjectStripsCodeFence(t *testing.T) {
	raw := "```json\n{\"result\":\"hello\"}\n```"
	if got := extractJSONObject(raw); got != "{\"result\":\"hello\"}" {
		t.Fatalf("extractJSONObject=%q", got)
	}
}

func TestTranslateOrExplainValidatesInput(t *testing.T) {
	if _, err := TranslateOrExplain(context.Background(), "", "", Request{Mode: "summarize", Text: "x"}); err == nil {
		t.Fatal("expected unsupported mode error")
	}
	if _, err := TranslateOrExplain(context.Background(), "", "", Request{Mode: "translate", Text: "  "}); err == nil {
		t.Fatal("expected empty text error")
	}
}

func TestTranslateUsesCacheAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization=%q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["response_format"] == nil {
			t.Fatal("expected JSON response_format")
		}
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"result\\\":\\\"translated\\\",\\\"terms\\\":[{\\\"source\\\":\\\"attention\\\",\\\"target\\\":\\\"attention-zh\\\"}]}\"}}]}"))
	}))
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL)
	t.Setenv("OPENAI_MODEL_NAME", "test-model")
	appconfig.ResetLLMConfigForTest()

	cacheDir := filepath.Join(t.TempDir(), "cache")
	first, err := TranslateOrExplain(context.Background(), cacheDir, "sha", Request{Mode: "translate", Text: "attention is all you need", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Result != "translated" || len(first.Terms) != 1 || first.Cached {
		t.Fatalf("first=%#v", first)
	}
	second, err := TranslateOrExplain(context.Background(), cacheDir, "sha", Request{Mode: "translate", Text: "attention is all you need", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached {
		t.Fatalf("expected cache hit, got %#v", second)
	}
}

func TestExplainAcceptsProseFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"self attention explanation\"}}]}"))
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL)
	t.Setenv("OPENAI_MODEL_NAME", "test-model")
	appconfig.ResetLLMConfigForTest()

	result, err := TranslateOrExplain(context.Background(), "", "", Request{Mode: "explain", Text: "self attention"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Result, "self attention") {
		t.Fatalf("result=%#v", result)
	}
}

package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSECommitsOnlyCompletedNativeEnvelope(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			raw := `{"id":"r1","status":"completed","output":[{"type":"compaction","encrypted_content":"opaque","future":{"v":1}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
				if complete {
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", raw)
				}
			}))
			defer server.Close()
			result, err := (Client{HTTP: server.Client()}).Create(context.Background(), server.URL, "key", Request{Model: "model", Stream: true})
			if !complete {
				if err == nil {
					t.Fatal("partial stream committed")
				}
				return
			}
			if err != nil || len(result.Output) != 2 || Text(result.Output) != "answer" || !strings.Contains(string(result.Output[0]), `"future"`) {
				t.Fatalf("native items lost: %#v %v", result, err)
			}
		})
	}
}

func TestCompactKeepsFullCanonicalOutputAndDoesNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "secret-body", 503)
			return
		}
		fmt.Fprint(w, `{"object":"response.compaction","output":[{"type":"message","role":"user","content":"retain me"},{"type":"compaction","encrypted_content":"opaque","extra":"keep"}]}`)
	}))
	defer server.Close()
	client := Client{HTTP: server.Client()}
	_, err := client.Compact(context.Background(), server.URL, "key", "model", "", nil)
	if err == nil || calls != 1 || strings.Contains(err.Error(), "secret-body") {
		t.Fatalf("request retried or error leaked: %d %v", calls, err)
	}
	result, err := client.Compact(context.Background(), server.URL, "key", "model", "", nil)
	if err != nil || len(result.Output) != 2 {
		t.Fatalf("retained items lost: %#v %v", result, err)
	}
	var item map[string]any
	_ = json.Unmarshal(result.Output[1], &item)
	if item["extra"] != "keep" {
		t.Fatal("unknown field stripped")
	}
}

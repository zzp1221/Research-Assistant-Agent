package intent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClassifierClassifiesText(t *testing.T) {
	var gotPath string
	var gotText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotText = payload.Text
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"intent_type":"Code_Execution","confidence":0.92,"entities":{"needs_plot":true,"output_mode":"plot"},"reasoning":"plot request","model":"test-model","latency_ms":12,"attempts":1}`))
	}))
	defer server.Close()

	classifier := NewHTTPClassifier(server.URL, time.Second)
	result, err := classifier.Classify(context.Background(), "帮我画一个对比图")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/predict" {
		t.Fatalf("path=%s, want /predict", gotPath)
	}
	if gotText != "帮我画一个对比图" {
		t.Fatalf("text=%q", gotText)
	}
	if result.IntentType != "Code_Execution" || result.Confidence != 0.92 {
		t.Fatalf("result=%#v", result)
	}
	if result.Entities["needs_plot"] != true || result.Reasoning != "plot request" {
		t.Fatalf("entities=%#v reasoning=%q", result.Entities, result.Reasoning)
	}
}

func TestHTTPClassifierReturnsErrorOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway down", http.StatusBadGateway)
	}))
	defer server.Close()

	classifier := NewHTTPClassifier(server.URL, time.Second)
	if _, err := classifier.Classify(context.Background(), "hello"); err == nil {
		t.Fatal("expected non-200 error")
	}
}

func TestNewHTTPClassifierEmptyURLReturnsNil(t *testing.T) {
	if classifier := NewHTTPClassifier("  ", time.Second); classifier != nil {
		t.Fatalf("classifier=%#v, want nil", classifier)
	}
}

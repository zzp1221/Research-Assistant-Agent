package llmclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
)

func TestHTTPRetriesCannotMultiplyLocalAndOuterRecovery(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "retry", 429)
			return
		}
		_, _ = io.WriteString(w, "recovered")
	}))
	defer server.Close()
	controller := recovery.New(models.RecoveryState{ChainID: "task", MaxActions: 2}, nil)
	ctx := recovery.WithController(context.Background(), controller)
	client := NewHTTPClient(Config{MaxAttempts: 8, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, AttemptTimeout: time.Second})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err = recovery.Reserve(ctx, "local_repair", nil); err != nil {
		t.Fatal(err)
	}
	if err = recovery.Reserve(ctx, "outer_retry", nil); err == nil {
		t.Fatal("outer layer multiplied spent retry budget")
	}
	state := controller.Snapshot()
	if attempts != 2 || state.HTTPAttempts != 2 || state.UsedActions != 2 {
		t.Fatalf("counters disagree: attempts=%d state=%+v", attempts, state)
	}
}

func TestAlwaysFailingHTTPStopsAtSharedAllowance(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts++; http.Error(w, "unavailable", 503) }))
	defer server.Close()
	ctx := recovery.WithController(context.Background(), recovery.New(models.RecoveryState{ChainID: "task", MaxActions: 1}, nil))
	client := NewHTTPClient(Config{MaxAttempts: 8, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, AttemptTimeout: time.Second})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if _, err := client.Do(req); err == nil || attempts != 2 {
		t.Fatalf("HTTP layers exceeded shared allowance: attempts=%d error=%v", attempts, err)
	}
}

package onboarding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-desktop/settings"
)

func TestValidate(t *testing.T) {
	if err := Validate(Credentials{}); err == nil {
		t.Fatal("empty credentials should fail")
	}
	if err := Validate(Credentials{BaseURL: "ftp://x", Model: "m", APIKey: "k"}); err == nil {
		t.Fatal("ftp should fail")
	}
	if err := Validate(Credentials{BaseURL: "https://api.example.com/v1", Model: "m", APIKey: "k"}); err != nil {
		t.Fatalf("valid credentials: %v", err)
	}
}

func TestTestConnection(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	client := &Client{}
	if err := client.TestConnection(context.Background(), Credentials{BaseURL: server.URL + "/v1", Model: "deepseek-chat", APIKey: "sk-test"}); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("authorization = %q", gotAuth)
	}
}

func TestTestConnectionRejectsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer server.Close()
	client := &Client{}
	if err := client.TestConnection(context.Background(), Credentials{BaseURL: server.URL, Model: "m", APIKey: "k"}); err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestFirstRunAndComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if !IsFirstRun(path) {
		t.Fatal("missing settings should be first run")
	}
	cfg := settings.Default()
	creds := Credentials{BaseURL: "https://api.example.com/v1", Model: "deepseek-chat", APIKey: "sk-x"}
	if err := Complete(path, creds, cfg); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if IsFirstRun(path) {
		t.Fatal("settings should exist after Complete")
	}
	loaded, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.OnboardingCompleted || loaded.LLM.Model != "deepseek-chat" {
		t.Fatalf("loaded = %#v", loaded)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

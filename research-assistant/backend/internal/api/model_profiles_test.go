package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"

	"github.com/gin-gonic/gin"
)

const modelProfileTestKey = "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE="

func setupModelProfileTest(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", modelProfileTestKey)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	t.Setenv("ADMIN_TOKEN", "admin-test-token")
	// Unit tests use example.com hostnames that may not resolve offline; the
	// explicit SSRF test disables this switch to exercise the real guard.
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	store, err := modelprofile.NewStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	resolver := modelresolver.New(store)
	modelresolver.SetDefault(resolver)
	t.Cleanup(func() { modelresolver.SetDefault(nil) })

	engine := gin.New()
	group := engine.Group("/api")
	registerModelProfileRoutes(group)
	return engine
}

func doModelProfileRequest(t *testing.T, engine *gin.Engine, method, path, user string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		payload = encoded
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-User-Id", user)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeModelProfile(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return decoded
}

func TestModelProfileAPIHidesSecretAndScopesOwnership(t *testing.T) {
	engine := setupModelProfileTest(t)
	create := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", map[string]any{
		"name": "My Vision Model", "provider": "openai_compatible", "base_url": "https://api.example.com/v1",
		"model": "vision-1", "api_key": "sk-verysecret9999", "roles": []string{"main", "vision"},
	}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	body := create.Body.String()
	if strings.Contains(body, "sk-verysecret9999") || strings.Contains(body, "ciphertext") {
		t.Fatalf("create response leaks secret: %s", body)
	}
	payload := decodeModelProfile(t, create)
	if payload["api_key_masked"] != "****9999" {
		t.Fatalf("masked = %v", payload["api_key_masked"])
	}

	list := doModelProfileRequest(t, engine, http.MethodGet, "/api/model-profiles", "alice", nil, nil)
	profiles := decodeModelProfile(t, list)["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("alice profiles = %d", len(profiles))
	}
	other := doModelProfileRequest(t, engine, http.MethodGet, "/api/model-profiles", "bob", nil, nil)
	if got := len(decodeModelProfile(t, other)["profiles"].([]any)); got != 0 {
		t.Fatalf("bob must not see alice profile, got %d", got)
	}
	id := payload["id"].(string)
	forbidden := doModelProfileRequest(t, engine, http.MethodPut, "/api/model-profiles/"+id, "bob", map[string]any{"name": "hijack"}, nil)
	if forbidden.Code != http.StatusNotFound {
		t.Fatalf("cross-owner update status=%d", forbidden.Code)
	}
	deleted := doModelProfileRequest(t, engine, http.MethodDelete, "/api/model-profiles/"+id, "bob", nil, nil)
	if deleted.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete status=%d", deleted.Code)
	}
}

func TestModelProfileAPIGlobalRequiresAdmin(t *testing.T) {
	engine := setupModelProfileTest(t)
	payload := map[string]any{
		"name": "Global", "provider": "openai_compatible", "base_url": "https://global.example.com/v1",
		"model": "global-model", "api_key": "sk-global-0001", "global": true,
	}
	denied := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", payload, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("non-admin global create status=%d body=%s", denied.Code, denied.Body.String())
	}
	allowed := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", payload, map[string]string{"Authorization": "Bearer admin-test-token"})
	if allowed.Code != http.StatusCreated {
		t.Fatalf("admin global create status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	// A normal user can read the global profile but cannot modify it.
	list := doModelProfileRequest(t, engine, http.MethodGet, "/api/model-profiles", "bob", nil, nil)
	profiles := decodeModelProfile(t, list)["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("global profile must be visible to everyone, got %d", len(profiles))
	}
	globalID := profiles[0].(map[string]any)["id"].(string)
	update := doModelProfileRequest(t, engine, http.MethodPut, "/api/model-profiles/"+globalID, "bob", map[string]any{"name": "mine now"}, nil)
	if update.Code != http.StatusForbidden {
		t.Fatalf("non-admin global update status=%d", update.Code)
	}
}

func TestModelProfileAPIRejectsPrivateEndpoints(t *testing.T) {
	engine := setupModelProfileTest(t)
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "false")
	payload := map[string]any{
		"name": "SSRF", "provider": "openai_compatible", "base_url": "http://127.0.0.1:8080/v1",
		"model": "x", "api_key": "sk",
	}
	blocked := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", payload, nil)
	if blocked.Code != http.StatusBadRequest {
		t.Fatalf("private base_url status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), "ssrf_blocked") {
		t.Fatalf("expected ssrf_blocked code: %s", blocked.Body.String())
	}
}

func TestModelRoutingAPIValidatesReferences(t *testing.T) {
	engine := setupModelProfileTest(t)
	create := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", map[string]any{
		"name": "Main", "provider": "openai_compatible", "base_url": "https://api.example.com/v1",
		"model": "m", "api_key": "sk-123456", "roles": []string{"main"},
	}, nil)
	mainID := decodeModelProfile(t, create)["id"].(string)
	invalid := doModelProfileRequest(t, engine, http.MethodPut, "/api/model-routing", "alice", map[string]any{
		"main_profile_id": "does-not-exist",
	}, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid routing status=%d", invalid.Code)
	}
	saved := doModelProfileRequest(t, engine, http.MethodPut, "/api/model-routing", "alice", map[string]any{
		"main_profile_id": mainID, "vision_profile_ids": []string{mainID},
	}, nil)
	if saved.Code != http.StatusOK {
		t.Fatalf("save routing status=%d body=%s", saved.Code, saved.Body.String())
	}
	read := doModelProfileRequest(t, engine, http.MethodGet, "/api/model-routing", "alice", nil, nil)
	routing := decodeModelProfile(t, read)["routing"].(map[string]any)
	if routing["main_profile_id"] != mainID {
		t.Fatalf("routing = %#v", routing)
	}
	// A profile referenced by routing cannot be deleted.
	deleted := doModelProfileRequest(t, engine, http.MethodDelete, "/api/model-profiles/"+mainID, "alice", nil, nil)
	if deleted.Code != http.StatusConflict {
		t.Fatalf("delete referenced profile status=%d", deleted.Code)
	}
}

func TestModelProfileAPIProbeUsesTextEndpoint(t *testing.T) {
	engine := setupModelProfileTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"PONG"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
	}))
	defer server.Close()
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	create := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles", "alice", map[string]any{
		"name": "Probe", "provider": "openai_compatible", "base_url": server.URL,
		"model": "m", "api_key": "sk-123456",
	}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	id := decodeModelProfile(t, create)["id"].(string)
	probe := doModelProfileRequest(t, engine, http.MethodPost, "/api/model-profiles/"+id+"/test", "alice", nil, nil)
	if probe.Code != http.StatusOK {
		t.Fatalf("text probe status=%d body=%s", probe.Code, probe.Body.String())
	}
	if !strings.Contains(probe.Body.String(), "PONG") && !strings.Contains(probe.Body.String(), "ok") {
		t.Fatalf("probe body=%s", probe.Body.String())
	}
}

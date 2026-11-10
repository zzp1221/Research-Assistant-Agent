package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/models"
)

func TestAgentEvalAPIRejectsEmptyMalformedAndAmbiguousRequests(t *testing.T) {
	r := agentprofile.NewRegistry(agentprofile.NewMemoryStore())
	p, err := r.CreatePrompt("owner", "eval", "SUCCESS", "owner")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(resolvedUserContextKey, "owner") })
	RegisterAgentProfileRoutes(router.Group("/api"), r, nil)
	for _, body := range []string{"{", `{"cases":[]}`, `{"mode":"template","use_model":true,"cases":[{"check_rendering":true}]}`, `{"mode":"invalid","cases":[{"check_rendering":true}]}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/prompt-versions/"+p.ID+"/eval", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("request %s returned %d: %s", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAgentEvalAPIReportsUnscoredModelAsError(t *testing.T) {
	r := agentprofile.NewRegistry(agentprofile.NewMemoryStore())
	p, _ := r.CreatePrompt("owner", "eval", "SUCCESS", "owner")
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(resolvedUserContextKey, "owner") })
	RegisterAgentProfileRoutes(router.Group("/api"), r, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/prompt-versions/"+p.ID+"/eval", strings.NewReader(`{"mode":"template","cases":[{}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var payload struct {
		Result models.PromptEvalResult `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || payload.Result.Passed != 0 || payload.Result.Errors != 1 || payload.Result.ScoredRate != nil {
		t.Fatalf("API hid missing grader: %d %s", recorder.Code, recorder.Body.String())
	}
}

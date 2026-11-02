package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/wiki"

	"github.com/gin-gonic/gin"
)

func newWikiAPITest(t *testing.T) (*gin.Engine, *wiki.Builder, *memory.FileStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	mem := memory.NewMemoryStore()
	builder := wiki.NewBuilder(wiki.NewMemoryPaperStore(), mem, nil, nil, wiki.NewAliasStore(t.TempDir()))
	router := gin.New()
	RegisterWikiRoutes(router.Group("/api"), builder)
	return router, builder, mem
}

func doWikiRequest(t *testing.T, router *gin.Engine, method, path, userID string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, userID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func createWikiTestPaper(t *testing.T, router *gin.Engine, userID, title, doi, arxiv string) wiki.PaperPage {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"title": title, "year": 2017, "doi": doi, "arxiv_id": arxiv})
	response := doWikiRequest(t, router, http.MethodPost, "/api/wiki/papers", userID, payload)
	if response.Code != http.StatusCreated {
		t.Fatalf("create paper status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Paper wiki.PaperPage `json:"paper"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.Paper
}

func TestWikiPaperAPICreateGetOwnerIsolation(t *testing.T) {
	router, _, _ := newWikiAPITest(t)
	paper := createWikiTestPaper(t, router, "u1", "Attention Is All You Need", "10.48550/arXiv.1706.03762", "1706.03762")
	if paper.PaperID == "" || paper.DOI == "" {
		t.Fatalf("unexpected paper: %+v", paper)
	}
	detail := doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers/"+paper.PaperID, "u1", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	other := doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers/"+paper.PaperID, "u2", nil)
	if other.Code != http.StatusNotFound {
		t.Fatalf("cross-owner detail status=%d body=%s", other.Code, other.Body.String())
	}
	list := doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers?q=attention", "u1", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var listed struct {
		Papers []wiki.PaperPage `json:"papers"`
		Total  int              `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if listed.Total != 1 || len(listed.Papers) != 1 {
		t.Fatalf("list total=%d papers=%d", listed.Total, len(listed.Papers))
	}
}

func TestWikiPointAPICreateListDelete(t *testing.T) {
	router, _, _ := newWikiAPITest(t)
	paper := createWikiTestPaper(t, router, "u1", "Attention Is All You Need", "", "1706.03762")
	create := doWikiRequest(t, router, http.MethodPost, "/api/wiki/papers/"+paper.PaperID+"/points", "u1", []byte("{\"text\":\"复现时注意 BLEU 27.3\"}"))
	if create.Code != http.StatusCreated {
		t.Fatalf("create point status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Point wiki.Point `json:"point"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	if created.Point.Kind != wiki.PointUserNote {
		t.Fatalf("unexpected point: %+v", created.Point)
	}
	list := doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers/"+paper.PaperID+"/points", "u1", nil)
	var listed struct {
		Points []wiki.Point `json:"points"`
		Total  int          `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if listed.Total != 1 {
		t.Fatalf("points total=%d", listed.Total)
	}
	deleted := doWikiRequest(t, router, http.MethodDelete, "/api/wiki/points/"+created.Point.Record.ID, "u1", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete point status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	list = doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers/"+paper.PaperID+"/points", "u1", nil)
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if listed.Total != 0 {
		t.Fatalf("soft-deleted point still listed: %d", listed.Total)
	}
}

func TestWikiLinkAPIListAndReject(t *testing.T) {
	router, builder, _ := newWikiAPITest(t)
	first := createWikiTestPaper(t, router, "u1", "Paper A", "", "1706.00001")
	second := createWikiTestPaper(t, router, "u1", "Paper B", "", "1706.00002")
	ctx := context.Background()
	for _, paper := range []wiki.PaperPage{first, second} {
		page, err := builder.Store.Get(ctx, "u1", paper.PaperID)
		if err != nil {
			t.Fatalf("get page: %v", err)
		}
		page.Entities = append(page.Entities, memory.Entity{Type: "dataset", Value: "wmt14"})
		if _, err := builder.Store.Put(ctx, page); err != nil {
			t.Fatalf("put page: %v", err)
		}
	}
	links, err := builder.BuildLinksForPaper(ctx, "u1", first.PaperID)
	if err != nil || len(links) != 1 {
		t.Fatalf("build links err=%v len=%d", err, len(links))
	}
	list := doWikiRequest(t, router, http.MethodGet, "/api/wiki/papers/"+first.PaperID+"/links", "u1", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("links status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Links []wiki.Link `json:"links"`
		Total int         `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if listed.Total != 1 || len(listed.Links) != 1 {
		t.Fatalf("links total=%d", listed.Total)
	}
	reject := doWikiRequest(t, router, http.MethodPost, "/api/wiki/links/"+listed.Links[0].ID+"/reject", "u1", []byte("{\"reason\":\"different dataset split\"}"))
	if reject.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", reject.Code, reject.Body.String())
	}
	var rejected struct {
		Link wiki.Link `json:"link"`
	}
	_ = json.Unmarshal(reject.Body.Bytes(), &rejected)
	if rejected.Link.Status != memory.StatusArchived {
		t.Fatalf("reject status=%q", rejected.Link.Status)
	}
}

func TestWikiStatsAndBuildAPI(t *testing.T) {
	router, _, _ := newWikiAPITest(t)
	createWikiTestPaper(t, router, "u1", "Attention Is All You Need", "", "1706.03762")
	stats := doWikiRequest(t, router, http.MethodGet, "/api/wiki/stats", "u1", nil)
	if stats.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", stats.Code, stats.Body.String())
	}
	var summary wiki.Stats
	_ = json.Unmarshal(stats.Body.Bytes(), &summary)
	if summary.Papers != 1 {
		t.Fatalf("stats papers=%d", summary.Papers)
	}
	build := doWikiRequest(t, router, http.MethodPost, "/api/wiki/build", "u1", []byte("{\"mode\":\"full\"}"))
	if build.Code != http.StatusAccepted {
		t.Fatalf("build status=%d body=%s", build.Code, build.Body.String())
	}
	var job struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(build.Body.Bytes(), &job)
	if job.JobID == "" {
		t.Fatalf("missing job id")
	}
}

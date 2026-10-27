package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateRemotePDFURLRejectsPrivateAndNonHTTPTargets(t *testing.T) {
	ctx := context.Background()
	cases := map[string]bool{
		"http://127.0.0.1/paper.pdf":             false,
		"http://localhost/paper.pdf":             false,
		"http://169.254.169.254/latest/meta":     false,
		"http://10.0.0.1/paper.pdf":              false,
		"file:///etc/passwd":                     false,
		"ftp://example.com/paper.pdf":            false,
		"http://user:pass@example.com/paper.pdf": false,
		"https://1.1.1.1/paper.pdf":              true,
	}
	for raw, wantOK := range cases {
		_, err := validateRemotePDFURL(ctx, raw)
		if wantOK && err != nil {
			t.Fatalf("expected %q to be accepted: %v", raw, err)
		}
		if !wantOK && err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestUploadRejectsContentExtensionMismatch(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "fake.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("this is definitely not a pdf document"))
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/uploads", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(userIDHeaderName, "spoof-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHealthStaysPublicWhenAPITokenConfigured(t *testing.T) {
	t.Setenv("API_AUTH_TOKEN", "secret-token")
	t.Setenv("REQUIRE_API_AUTH", "true")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	group.Use(APIAuthMiddleware())
	group.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	group.GET("/plans", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"plans": []string{}}) })

	health := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	healthResponse := httptest.NewRecorder()
	router.ServeHTTP(healthResponse, health)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health without token status=%d", healthResponse.Code)
	}

	plans := httptest.NewRequest(http.MethodGet, "/api/plans", nil)
	plansResponse := httptest.NewRecorder()
	router.ServeHTTP(plansResponse, plans)
	if plansResponse.Code != http.StatusUnauthorized {
		t.Fatalf("plans without token status=%d", plansResponse.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/plans", nil)
	authorized.Header.Set("Authorization", "Bearer secret-token")
	authorizedResponse := httptest.NewRecorder()
	router.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("plans with token status=%d body=%s", authorizedResponse.Code, authorizedResponse.Body.String())
	}
}

func TestOpenAPIDocumentIsValid(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIDocument, &document); err != nil {
		t.Fatalf("embedded openapi is not valid JSON: %v", err)
	}
	if version, _ := document["openapi"].(string); !strings.HasPrefix(version, "3.") {
		t.Fatalf("unexpected openapi version %q", version)
	}
	paths, _ := document["paths"].(map[string]any)
	if len(paths) < 10 {
		t.Fatalf("openapi document is too small: %d paths", len(paths))
	}
	for _, required := range []string{"/api/plan", "/api/plans/{id}/execute", "/api/literature/search", "/api/metrics"} {
		if _, ok := paths[required]; !ok {
			t.Fatalf("openapi document is missing %s", required)
		}
	}
}

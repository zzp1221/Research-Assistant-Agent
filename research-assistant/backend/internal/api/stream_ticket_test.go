package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestStreamTicketRoundTripRejectsTamperingAndExpiry(t *testing.T) {
	now := time.Now()
	ticket, expiresAt := issueStreamTicket("plan-1", now)
	if expiresAt.Before(now) {
		t.Fatal("ticket expiry must be in the future")
	}
	if !validateStreamTicket(ticket, "plan-1", now) {
		t.Fatal("expected issued ticket to validate")
	}
	if validateStreamTicket(ticket, "plan-2", now) {
		t.Fatal("ticket must be scoped to its plan")
	}
	if validateStreamTicket(ticket, "plan-1", now.Add(2*streamTicketTTL)) {
		t.Fatal("expired ticket must be rejected")
	}
	if validateStreamTicket(ticket+"x", "plan-1", now) {
		t.Fatal("tampered ticket must be rejected")
	}
}

func TestAPIAuthMiddlewareAcceptsStreamTicketWithoutBearerHeader(t *testing.T) {
	t.Setenv("API_AUTH_TOKEN", "deployment-secret")
	ticket, _ := issueStreamTicket("plan-1", time.Now())
	router := gin.New()
	router.Use(APIAuthMiddleware())
	router.GET("/api/plans/:id/stream", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/api/plans/plan-1/stream?ticket="+ticket, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("ticket status=%d body=%s", response.Code, response.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/plans/plan-1/stream", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing-ticket status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
}

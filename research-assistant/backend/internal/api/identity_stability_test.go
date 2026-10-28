package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestResolveUserIDIsStableWithinRequest guards the anonymous identity contract:
// calling the helper twice in one request must not mint two identities, because
// the second Set-Cookie would silently replace the first and orphan any row
// created during the same handler.
func TestResolveUserIDIsStableWithinRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "false")
	engine := gin.New()
	var first, second string
	engine.GET("/probe", func(c *gin.Context) {
		first = resolveUserID(c)
		second = resolveUserID(c)
		c.JSON(http.StatusOK, gin.H{"first": first, "second": second})
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if first == "" || second == "" {
		t.Fatalf("identity must not be empty: %q %q", first, second)
	}
	if first != second {
		t.Fatalf("identity changed inside one request: %q != %q", first, second)
	}
	setCookie := recorder.Result().Header.Values("Set-Cookie")
	count := 0
	for _, cookie := range setCookie {
		if len(cookie) >= len(anonUserCookieName)+1 && cookie[:len(anonUserCookieName)+1] == anonUserCookieName+"=" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("handler issued %d identity cookies, want at most 1: %v", count, setCookie)
	}
}

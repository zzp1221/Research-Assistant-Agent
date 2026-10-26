package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain keeps the historical API test suite working: those tests drive
// identity through the explicit legacy header switch. Production never enables
// this switch, and TestForgedUserHeaderIsIgnoredByDefault proves the default.
func TestMain(m *testing.M) {
	_ = os.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	gin.SetMode(gin.TestMode)
	code := m.Run()
	_ = os.Unsetenv("ALLOW_LEGACY_USER_HEADER")
	os.Exit(code)
}

func TestForgedUserHeaderIsIgnoredByDefault(t *testing.T) {
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "false")
	gin.SetMode(gin.TestMode)

	router := gin.New()
	var seen string
	router.GET("/who", func(c *gin.Context) {
		seen = resolveUserID(c)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/who", nil)
	req.Header.Set(userIDHeaderName, "admin-victim")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if seen == "admin-victim" {
		t.Fatalf("forged X-User-Id header was trusted as identity")
	}
	if strings.TrimSpace(seen) == "" {
		t.Fatalf("server did not issue an identity")
	}
	cookies := recorder.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == anonUserCookieName {
			found = true
			if !strings.Contains(cookie.Value, ".") {
				t.Fatalf("identity cookie is not signed: %q", cookie.Value)
			}
			if verifyIdentityCookie(cookie.Value) != seen {
				t.Fatalf("signed cookie does not verify to the issued identity")
			}
		}
	}
	if !found {
		t.Fatalf("server identity cookie %q was not set", anonUserCookieName)
	}
}

func TestForgedIdentityCookieIsRejected(t *testing.T) {
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "false")
	gin.SetMode(gin.TestMode)

	router := gin.New()
	var seen string
	router.GET("/who", func(c *gin.Context) {
		seen = resolveUserID(c)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/who", nil)
	req.AddCookie(&http.Cookie{Name: anonUserCookieName, Value: "admin-victim.deadbeef"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if seen == "admin-victim" {
		t.Fatalf("forged signed cookie was accepted")
	}
	if strings.TrimSpace(seen) == "" {
		t.Fatalf("server did not issue a fresh identity")
	}
}

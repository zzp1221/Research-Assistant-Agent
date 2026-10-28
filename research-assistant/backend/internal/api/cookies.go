package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	anonUserCookieName = "sa_uid"
	sessionCookieName  = "sa_sid"
	userIDHeaderName   = "X-User-Id"
	sessionHeaderName  = "X-Session-Id"
)

var identitySecretOnce struct {
	sync.Once
	secret []byte
}

// identityCookieSecret derives the HMAC key used to sign identity cookies.
// A dedicated IDENTITY_COOKIE_SECRET is preferred; API_AUTH_TOKEN is used as a
// fallback so a single-node deployment does not need an extra secret. A random
// per-process secret is the last resort (identities then reset on restart).
func identityCookieSecret() []byte {
	identitySecretOnce.Do(func() {
		if raw := strings.TrimSpace(os.Getenv("IDENTITY_COOKIE_SECRET")); raw != "" {
			identitySecretOnce.secret = []byte(raw)
			return
		}
		if raw := strings.TrimSpace(os.Getenv("API_AUTH_TOKEN")); raw != "" {
			identitySecretOnce.secret = []byte("identity:" + raw)
			return
		}
		buffer := make([]byte, 32)
		if _, err := rand.Read(buffer); err != nil {
			identitySecretOnce.secret = []byte("identity-fallback-secret")
			return
		}
		identitySecretOnce.secret = buffer
	})
	return identitySecretOnce.secret
}

func identityMAC(value string) string {
	mac := hmac.New(sha256.New, identityCookieSecret())
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// signIdentityCookie returns "<value>.<hmac>" so the server can verify that an
// identity cookie was issued by this backend and was not forged by a client.
func signIdentityCookie(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return value + "." + identityMAC(value)
}

// verifyIdentityCookie validates the HMAC and returns the embedded identity.
func verifyIdentityCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	idx := strings.LastIndex(raw, ".")
	if idx <= 0 || idx >= len(raw)-1 {
		return ""
	}
	value := raw[:idx]
	signature := raw[idx+1:]
	expected := identityMAC(value)
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
		return ""
	}
	return sanitizeIdentityValue(value)
}

// legacyUserHeaderAllowed is a test-only compatibility switch. Production must
// never trust a client supplied X-User-Id header.
func legacyUserHeaderAllowed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_LEGACY_USER_HEADER")), "true")
}

func ensureAnonUserIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(anonUserCookieName); err == nil {
		if verified := verifyIdentityCookie(v); verified != "" {
			return verified
		}
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, anonUserCookieName, signIdentityCookie(id), cookieMaxAgeSeconds())
	return id
}

const (
	resolvedUserContextKey    = "resolved_user_id"
	resolvedSessionContextKey = "resolved_session_id"
)

// resolveUserID returns the authenticated principal of the request. The value
// comes exclusively from a server-signed HttpOnly cookie; a bare X-User-Id
// header is ignored (unless the explicit test-only switch is enabled).
//
// The result is memoized on the gin context: a handler that calls this helper
// twice must not mint two different anonymous identities (each call would
// otherwise issue a new cookie and the later Set-Cookie would win).
func resolveUserID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if cached, ok := c.Get(resolvedUserContextKey); ok {
		if value, ok := cached.(string); ok && value != "" {
			return value
		}
	}
	if legacyUserHeaderAllowed() {
		if headerValue := sanitizeIdentityValue(c.GetHeader(userIDHeaderName)); headerValue != "" {
			setHTTPOnlyCookie(c, anonUserCookieName, signIdentityCookie(headerValue), cookieMaxAgeSeconds())
			c.Set(resolvedUserContextKey, headerValue)
			return headerValue
		}
	}
	resolved := ensureAnonUserIDCookie(c)
	if resolved != "" {
		c.Set(resolvedUserContextKey, resolved)
	}
	return resolved
}

func ensureSessionIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(sessionCookieName); err == nil {
		if verified := verifyIdentityCookie(v); verified != "" {
			return verified
		}
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, sessionCookieName, signIdentityCookie(id), cookieMaxAgeSeconds())
	return id
}

// resolveSessionID returns the server-signed session id. Client supplied
// X-Session-Id headers are only honoured in the explicit legacy test mode.
func resolveSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if cached, ok := c.Get(resolvedSessionContextKey); ok {
		if value, ok := cached.(string); ok && value != "" {
			return value
		}
	}
	if legacyUserHeaderAllowed() {
		if headerValue := sanitizeIdentityValue(c.GetHeader(sessionHeaderName)); headerValue != "" {
			setHTTPOnlyCookie(c, sessionCookieName, signIdentityCookie(headerValue), cookieMaxAgeSeconds())
			c.Set(resolvedSessionContextKey, headerValue)
			return headerValue
		}
	}
	resolved := ensureSessionIDCookie(c)
	if resolved != "" {
		c.Set(resolvedSessionContextKey, resolved)
	}
	return resolved
}

func cookieMaxAgeSeconds() int {
	const defaultMaxAge = int((7 * 24 * time.Hour) / time.Second)
	if v := strings.TrimSpace(os.Getenv("INTENT_SESSION_COOKIE_MAX_AGE_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxAge
}

func setHTTPOnlyCookie(c *gin.Context, name, value string, maxAgeSeconds int) {
	if c == nil {
		return
	}
	// 本地开发默认 Lax，避免跨域 Cookie 场景下被浏览器直接丢弃。
	c.SetSameSite(parseSameSite(os.Getenv("INTENT_COOKIE_SAMESITE")))
	c.SetCookie(name, value, maxAgeSeconds, "/", cookieDomain(), cookieSecure(c), true)
}

func cookieDomain() string {
	return strings.TrimSpace(os.Getenv("INTENT_COOKIE_DOMAIN"))
}

func cookieSecure(c *gin.Context) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("INTENT_COOKIE_SECURE")), "true") {
		return true
	}
	return c != nil && c.Request != nil && c.Request.TLS != nil
}

func parseSameSite(raw string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}

func sanitizeIdentityValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	builder := strings.Builder{}
	builder.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '@':
			builder.WriteRune(r)
		}
		if builder.Len() >= 128 {
			break
		}
	}
	return builder.String()
}

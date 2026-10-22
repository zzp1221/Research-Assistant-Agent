package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const streamTicketTTL = 60 * time.Second

var streamTicketSecret = func() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return []byte("research-assistant-stream-ticket-fallback")
	}
	return secret
}()

func APIAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := strings.TrimSpace(os.Getenv("API_AUTH_TOKEN"))
		if c.Request.URL.Path == "/api/health" {
			c.Next()
			return
		}
		if expected == "" {
			if strings.EqualFold(strings.TrimSpace(os.Getenv("REQUIRE_API_AUTH")), "true") {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "API_AUTH_TOKEN is required but not configured"})
				return
			}
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet && strings.HasSuffix(c.Request.URL.Path, "/stream") {
			if planID := planIDFromStreamPath(c.Request.URL.Path); planID != "" &&
				validateStreamTicket(c.Query("ticket"), planID, time.Now()) {
				c.Next()
				return
			}
		}
		provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid bearer token"})
			return
		}
		c.Next()
	}
}

func planIDFromStreamPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "plans" && parts[3] == "stream" {
		return parts[2]
	}
	return ""
}

func streamTicketPayload(planID string, expiry int64) string {
	return planID + "|" + strconv.FormatInt(expiry, 10)
}

func streamTicketMAC(payload string) string {
	mac := hmac.New(sha256.New, streamTicketSecret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func issueStreamTicket(planID string, now time.Time) (string, time.Time) {
	expiry := now.Add(streamTicketTTL).Unix()
	payload := streamTicketPayload(planID, expiry)
	raw := payload + "|" + streamTicketMAC(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), time.Unix(expiry, 0).UTC()
}

func validateStreamTicket(ticket, planID string, now time.Time) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(ticket))
	if err != nil {
		return false
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 || parts[0] != planID {
		return false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.After(time.Unix(expiry, 0)) {
		return false
	}
	payload := streamTicketPayload(parts[0], expiry)
	return subtle.ConstantTimeCompare([]byte(parts[2]), []byte(streamTicketMAC(payload))) == 1
}

func validateRemotePDFURL(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid PDF URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("PDF URL must use http or https")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("PDF URL must not contain user information")
	}
	if _, err := resolvePublicIPs(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func safeRemoteTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid remote address: %w", err)
			}
			addresses, err := resolvePublicIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, fmt.Errorf("connect to remote PDF host: %w", lastErr)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
}

func resolvePublicIPs(ctx context.Context, hostname string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("cannot resolve PDF host")
	}
	public := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if blockedRemoteIP(address.IP) {
			return nil, fmt.Errorf("PDF URL resolves to a private or local address")
		}
		public = append(public, address.IP)
	}
	return public, nil
}

func blockedRemoteIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

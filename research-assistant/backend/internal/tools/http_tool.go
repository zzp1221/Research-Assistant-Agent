package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// HTTPInvoker calls a registered HTTP JSON tool.
type HTTPInvoker struct {
	Client *http.Client
}

// Invoke posts the arguments as JSON to the tool endpoint.
func (h *HTTPInvoker) Invoke(ctx context.Context, tool models.ToolDefinition, arguments map[string]any, dryRun bool) (*NativeResult, error) {
	if tool.HTTP == nil || strings.TrimSpace(tool.HTTP.URL) == "" {
		return nil, &NativeError{Code: "tool_unavailable", Message: "http tool has no url"}
	}
	if err := ValidateOutboundURL(tool.HTTP.URL, tool.HTTP.AllowPrivateHosts); err != nil {
		return nil, &NativeError{Code: "tool_ssrf_blocked", Message: err.Error()}
	}
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, &NativeError{Code: "tool_invalid_arguments", Message: err.Error()}
	}
	method := strings.ToUpper(strings.TrimSpace(tool.HTTP.Method))
	if method == "" {
		method = http.MethodPost
	}
	if dryRun {
		return &NativeResult{
			Text: fmt.Sprintf("dry-run: would %s %s with %d bytes", method, tool.HTTP.URL, len(body)),
			JSON: map[string]any{"dry_run": true, "url": tool.HTTP.URL, "bytes": len(body)},
			Bytes: int64(len(body)),
		}, nil
	}
	request, err := http.NewRequestWithContext(ctx, method, tool.HTTP.URL, bytes.NewReader(body))
	if err != nil {
		return nil, &NativeError{Code: "tool_invalid_arguments", Message: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, sealed := range tool.HTTP.HeaderSealed {
		plain, err := openSecretValue(sealed, "http:"+tool.ID, tool.OwnerID)
		if err != nil {
			return nil, &NativeError{Code: "tool_unavailable", Message: "open header " + key + ": " + err.Error()}
		}
		request.Header.Set(key, plain)
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	defer response.Body.Close()
	maxBytes := tool.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, &NativeError{Code: "tool_execution_failed", Message: err.Error()}
	}
	truncated := false
	if int64(len(raw)) > maxBytes {
		raw = raw[:maxBytes]
		truncated = true
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &NativeError{Code: "tool_execution_failed", Message: fmt.Sprintf("http tool returned %d: %s", response.StatusCode, preview(string(raw), 200))}
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			text, _ := json.Marshal(decoded)
			return &NativeResult{Text: string(text), JSON: decoded, Truncated: truncated, Bytes: int64(len(raw))}, nil
		}
	}
	return &NativeResult{Text: string(raw), Truncated: truncated, Bytes: int64(len(raw))}, nil
}

// ValidateOutboundURL rejects SSRF targets unless the host is explicitly
// allowlisted or the tool is an admin-approved private-host tool.
func ValidateOutboundURL(rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("only http/https urls are supported")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if allowPrivate || hostAllowlisted(host) {
		return nil
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("plain http is not allowed for a non-allowlisted host (private/loopback targets require explicit allowlisting)")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host %s: %w", host, err)
	}
	for _, ip := range ips {
		if IsPrivateIP(ip) {
			return fmt.Errorf("host %s resolves to a private address (%s); configure MCP_HTTP_ALLOWED_HOSTS or allow_private_hosts", host, ip.String())
		}
	}
	return nil
}

// IsPrivateIP reports loopback, private, link-local, multicast and
// unspecified addresses.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be judged by its v4 form.
	if v4 := ip.To4(); v4 != nil {
		return net.ParseIP(v4.String()).IsLoopback() || net.ParseIP(v4.String()).IsPrivate()
	}
	return false
}

func hostAllowlisted(host string) bool {
	raw := strings.TrimSpace(os.Getenv("MCP_HTTP_ALLOWED_HOSTS"))
	if raw == "" {
		return false
	}
	for _, candidate := range strings.Split(raw, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && strings.EqualFold(candidate, host) {
			return true
		}
	}
	return false
}

// openSecretValue decrypts a sealed value bound to a scope/owner.
func openSecretValue(sealed, scopeID, ownerID string) (string, error) {
	if strings.TrimSpace(sealed) == "" {
		return "", nil
	}
	return openSecret(sealed, scopeID, ownerID)
}

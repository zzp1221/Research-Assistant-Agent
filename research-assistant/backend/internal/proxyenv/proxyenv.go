// Package proxyenv normalizes proxy environment variables for the server and
// desktop entrypoints. The historical behavior cleared all proxy variables,
// which breaks corporate networks; desktop builds can opt into preserving and
// importing the Windows system proxy with RA_KEEP_SYSTEM_PROXY=1.
package proxyenv

import (
	"os"
	"strings"
)

var proxyKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "all_proxy",
}

// Apply clears or preserves proxy variables according to RA_KEEP_SYSTEM_PROXY.
func Apply() {
	if envBool("RA_KEEP_SYSTEM_PROXY") {
		applySystemProxy()
	} else {
		for _, key := range proxyKeys {
			_ = os.Unsetenv(key)
		}
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		_ = os.Setenv(key, mergeNoProxy(os.Getenv(key)))
	}
}

func mergeNoProxy(existing string) string {
	defaults := []string{"localhost", "127.0.0.1", "::1"}
	if strings.TrimSpace(existing) == "" {
		return strings.Join(defaults, ",")
	}
	parts := strings.Split(existing, ",")
	seen := map[string]struct{}{}
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			seen[strings.ToLower(trimmed)] = struct{}{}
		}
	}
	for _, item := range defaults {
		if _, ok := seen[item]; !ok {
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, ",")
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

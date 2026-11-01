//go:build windows

package proxyenv

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func applySystemProxy() {
	server, ok := windowsSystemProxy()
	if !ok {
		return
	}
	setIfEmpty("HTTP_PROXY", proxyForScheme(server, "http"))
	setIfEmpty("HTTPS_PROXY", proxyForScheme(server, "https"))
	setIfEmpty("http_proxy", proxyForScheme(server, "http"))
	setIfEmpty("https_proxy", proxyForScheme(server, "https"))
}

func windowsSystemProxy() (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings", registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return "", false
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return "", false
	}
	return strings.TrimSpace(server), true
}

func proxyForScheme(server, scheme string) string {
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, "="); idx > 0 {
			if strings.EqualFold(strings.TrimSpace(part[:idx]), scheme) {
				return normalizeProxyURL(strings.TrimSpace(part[idx+1:]))
			}
			continue
		}
		return normalizeProxyURL(part)
	}
	return ""
}

func normalizeProxyURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return value
	}
	return "http://" + value
}

func setIfEmpty(key, value string) {
	if value == "" {
		return
	}
	if strings.TrimSpace(os.Getenv(key)) == "" {
		_ = os.Setenv(key, value)
	}
}

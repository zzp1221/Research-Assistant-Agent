// Package proxy serves the embedded frontend and forwards /api requests to the
// backend over a fixed loopback origin. SSE responses are flushed immediately.
package proxy

import (
	"bytes"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// Config configures the desktop reverse proxy.
type Config struct {
	BackendURL string
	StaticFS   fs.FS
}

// New builds the proxy handler.
func New(cfg Config) (http.Handler, error) {
	target, err := url.Parse(cfg.BackendURL)
	if err != nil {
		return nil, err
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	// SSE must not be buffered by the proxy.
	reverse.FlushInterval = -1
	reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "backend unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return &handler{backend: reverse, static: cfg.StaticFS}, nil
}

type handler struct {
	backend http.Handler
	static  fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			originURL, err := url.Parse(origin)
			if err != nil || !sameHost(originURL.Host, r.Host) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
	}
	if isAPIPath(r.URL.Path) {
		h.backend.ServeHTTP(w, r)
		return
	}
	if h.static == nil {
		http.NotFound(w, r)
		return
	}
	assetPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if assetPath == "" {
		assetPath = "index.html"
	}
	if info, err := fs.Stat(h.static, assetPath); err == nil && !info.IsDir() {
		serveStatic(w, r, h.static, assetPath)
		return
	}
	serveStatic(w, r, h.static, "index.html")
}

func serveStatic(w http.ResponseWriter, r *http.Request, static fs.FS, assetPath string) {
	data, err := fs.ReadFile(static, assetPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, assetPath, time.Time{}, bytes.NewReader(data))
}

func isAPIPath(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || requestPath == "/ping"
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func sameHost(a, b string) bool {
	hostA := a
	if h, _, err := net.SplitHostPort(a); err == nil {
		hostA = h
	}
	hostB := b
	if h, _, err := net.SplitHostPort(b); err == nil {
		hostB = h
	}
	return strings.EqualFold(strings.Trim(hostA, "[]"), strings.Trim(hostB, "[]"))
}

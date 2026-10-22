package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// runGateway is the sandbox control plane. It deliberately has no Docker
// socket and no host workspace mount: it authenticates callers and forwards
// high-level sandbox requests to the isolated executor.
func runGateway() {
	upstream := strings.TrimSpace(os.Getenv("SANDBOX_UPSTREAM_URL"))
	if upstream == "" {
		upstream = "http://docker-sandbox-executor:8082"
	}
	handler, err := newGatewayHandler(upstream)
	if err != nil {
		log.Fatalf("configure sandbox gateway: %v", err)
	}
	listenAddr := sandboxListenAddr()
	log.Printf("Docker Sandbox Gateway starting on %s, upstream=%s", listenAddr, upstream)
	if err := http.ListenAndServe(listenAddr, handler); err != nil {
		log.Fatalf("sandbox gateway stopped: %v", err)
	}
}

func newGatewayHandler(upstream string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid SANDBOX_UPSTREAM_URL %q", upstream)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
		log.Printf("sandbox gateway upstream error: %v", proxyErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"sandbox executor unavailable"}`))
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", gatewayAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})))
	return mux, nil
}

func gatewayAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		expected := strings.TrimSpace(os.Getenv("SANDBOX_API_TOKEN"))
		if expected == "" {
			http.Error(w, `{"error":"SANDBOX_API_TOKEN is required"}`, http.StatusServiceUnavailable)
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			http.Error(w, `{"error":"invalid sandbox bearer token"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

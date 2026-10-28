package webread

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// resolvePublicIPs resolves hostname and rejects any non-public address.
func resolvePublicIPs(ctx context.Context, hostname string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("cannot resolve host")
	}
	public := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if blockedIP(address.IP) {
			return nil, fmt.Errorf("host resolves to a private or local address")
		}
		public = append(public, address.IP)
	}
	return public, nil
}

func blockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// safeTransport dials only resolved public addresses, preventing DNS rebinding
// between validation and connection.
func safeTransport(allowPrivate bool) *http.Transport {
	if allowPrivate {
		return &http.Transport{Proxy: nil}
	}
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
			return nil, fmt.Errorf("connect to web host: %w", lastErr)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
}

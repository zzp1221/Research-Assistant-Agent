package shell

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MetricsSummary is the compact local metrics payload shown in the UI.
type MetricsSummary struct {
	Goroutines          int     `json:"goroutines"`
	ResidentMemoryBytes int64   `json:"resident_memory_bytes"`
	RequestCount        float64 `json:"request_count"`
	CollectedAt         string  `json:"collected_at"`
}

// ParsePrometheusMetrics extracts the small set of gauges the desktop shows.
func ParsePrometheusMetrics(text string, now time.Time) MetricsSummary {
	summary := MetricsSummary{CollectedAt: now.UTC().Format(time.RFC3339)}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if index := strings.IndexByte(name, '{'); index >= 0 {
			name = name[:index]
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch name {
		case "go_goroutines":
			summary.Goroutines = int(value)
		case "process_resident_memory_bytes":
			summary.ResidentMemoryBytes = int64(value)
		case "http_requests_total", "http_request_total":
			summary.RequestCount += value
		}
	}
	return summary
}

// FetchMetrics reads the backend Prometheus endpoint through the loopback
// origin and returns a compact summary.
func (s *Shell) FetchMetrics(ctx context.Context) (MetricsSummary, error) {
	s.mu.RLock()
	backendURL := s.status.BackendURL
	token := s.backendTok
	s.mu.RUnlock()
	if strings.TrimSpace(backendURL) == "" {
		return MetricsSummary{}, fmt.Errorf("backend is not running")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(backendURL, "/")+"/metrics", nil)
	if err != nil {
		return MetricsSummary{}, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return MetricsSummary{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MetricsSummary{}, fmt.Errorf("metrics returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return MetricsSummary{}, err
	}
	return ParsePrometheusMetrics(string(body), time.Now()), nil
}

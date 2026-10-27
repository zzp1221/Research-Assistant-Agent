// Package metrics is a dependency-free Prometheus-compatible metrics registry.
//
// The project deliberately avoids the Prometheus client dependency: the
// deployment only needs a text exposition endpoint, and keeping the registry
// local makes it trivial to unit test without a server.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Labels is a set of metric dimensions.
type Labels map[string]string

// Registry stores counters, gauges and histograms.
type Registry struct {
	mu         sync.RWMutex
	types      map[string]string
	help       map[string]string
	counters   map[string]map[string]float64
	gauges     map[string]map[string]float64
	histograms map[string]map[string]*histogram
	buckets    map[string][]float64
	startedAt  time.Time
}

type histogram struct {
	Buckets []float64
	Counts  []uint64
	Sum     float64
	Count   uint64
}

// DefaultBuckets covers request latencies from milliseconds to half an hour.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1800}

func NewRegistry() *Registry {
	return &Registry{
		types:      map[string]string{},
		help:       map[string]string{},
		counters:   map[string]map[string]float64{},
		gauges:     map[string]map[string]float64{},
		histograms: map[string]map[string]*histogram{},
		buckets:    map[string][]float64{},
		startedAt:  time.Now(),
	}
}

var defaultRegistry = NewRegistry()

// Default returns the process-wide registry.
func Default() *Registry { return defaultRegistry }

// SetBuckets overrides the histogram buckets for one metric name.
func (r *Registry) SetBuckets(name string, buckets []float64) {
	if len(buckets) == 0 {
		return
	}
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets[name] = sorted
}

// Describe records the metric type and help text once.
func (r *Registry) Describe(name, metricType, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[name]; !exists {
		r.types[name] = metricType
		r.help[name] = help
	}
}

// Inc adds one to a counter.
func (r *Registry) Inc(name string, labels Labels) { r.Add(name, 1, labels) }

// Add increments a counter by value.
func (r *Registry) Add(name string, value float64, labels Labels) {
	if value == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureType(name, "counter")
	series := r.counters[name]
	if series == nil {
		series = map[string]float64{}
		r.counters[name] = series
	}
	key := labelKey(labels)
	series[key] += value
}

// Set sets a gauge value.
func (r *Registry) Set(name string, value float64, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureType(name, "gauge")
	series := r.gauges[name]
	if series == nil {
		series = map[string]float64{}
		r.gauges[name] = series
	}
	series[labelKey(labels)] = value
}

// AddGauge increments or decrements a gauge.
func (r *Registry) AddGauge(name string, delta float64, labels Labels) {
	if delta == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureType(name, "gauge")
	series := r.gauges[name]
	if series == nil {
		series = map[string]float64{}
		r.gauges[name] = series
	}
	key := labelKey(labels)
	series[key] += delta
}

// Observe records a histogram sample.
func (r *Registry) Observe(name string, value float64, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureType(name, "histogram")
	buckets := r.buckets[name]
	if len(buckets) == 0 {
		buckets = DefaultBuckets
	}
	series := r.histograms[name]
	if series == nil {
		series = map[string]*histogram{}
		r.histograms[name] = series
	}
	key := labelKey(labels)
	h := series[key]
	if h == nil {
		h = &histogram{Buckets: buckets, Counts: make([]uint64, len(buckets))}
		series[key] = h
	}
	for i, upper := range buckets {
		if value <= upper {
			h.Counts[i]++
		}
	}
	h.Sum += value
	h.Count++
}

func (r *Registry) ensureType(name, metricType string) {
	if _, exists := r.types[name]; !exists {
		r.types[name] = metricType
	}
}

// CounterValue returns the current counter value (tests).
func (r *Registry) CounterValue(name string, labels Labels) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.counters[name][labelKey(labels)]
}

// GaugeValue returns the current gauge value (tests).
func (r *Registry) GaugeValue(name string, labels Labels) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gauges[name][labelKey(labels)]
}

// HistogramCount returns the sample count for a histogram series (tests).
func (r *Registry) HistogramCount(name string, labels Labels) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if h := r.histograms[name][labelKey(labels)]; h != nil {
		return h.Count
	}
	return 0
}

// Reset clears all series (tests).
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = map[string]map[string]float64{}
	r.gauges = map[string]map[string]float64{}
	r.histograms = map[string]map[string]*histogram{}
}

func labelKey(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
	}
	return builder.String()
}

func formatLabels(key string, extraName, extraValue string, extraCount uint64, hasExtra bool) string {
	if key == "" && !hasExtra {
		return ""
	}
	parts := make([]string, 0, 4)
	if key != "" {
		for _, pair := range strings.Split(key, ",") {
			idx := strings.IndexByte(pair, '=')
			if idx < 0 {
				continue
			}
			parts = append(parts, escapeLabel(pair[:idx])+`="`+escapeValue(pair[idx+1:])+`"`)
		}
	}
	if hasExtra {
		parts = append(parts, escapeLabel(extraName)+`="`+escapeValue(extraValue)+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value)
}

func escapeValue(value string) string {
	return escapeLabel(value)
}

func formatFloat(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// Render produces the Prometheus text exposition format.
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var builder strings.Builder
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		metricType := r.types[name]
		if help := r.help[name]; help != "" {
			fmt.Fprintf(&builder, "# HELP %s %s\n", name, help)
		}
		fmt.Fprintf(&builder, "# TYPE %s %s\n", name, metricType)
		switch metricType {
		case "counter":
			for _, key := range sortedKeys(r.counters[name]) {
				fmt.Fprintf(&builder, "%s%s %s\n", name, formatLabels(key, "", "", 0, false), formatFloat(r.counters[name][key]))
			}
		case "gauge":
			for _, key := range sortedKeys(r.gauges[name]) {
				fmt.Fprintf(&builder, "%s%s %s\n", name, formatLabels(key, "", "", 0, false), formatFloat(r.gauges[name][key]))
			}
		case "histogram":
			for _, key := range sortedKeysHistogram(r.histograms[name]) {
				h := r.histograms[name][key]
				var cumulative uint64
				for i, upper := range h.Buckets {
					cumulative = h.Counts[i]
					fmt.Fprintf(&builder, "%s_bucket%s %d\n", name, formatLabels(key, "le", formatFloat(upper), 0, true), cumulative)
				}
				fmt.Fprintf(&builder, "%s_bucket%s %d\n", name, formatLabels(key, "le", "+Inf", 0, true), h.Count)
				fmt.Fprintf(&builder, "%s_sum%s %s\n", name, formatLabels(key, "", "", 0, false), formatFloat(h.Sum))
				fmt.Fprintf(&builder, "%s_count%s %d\n", name, formatLabels(key, "", "", 0, false), h.Count)
			}
		}
	}

	// Process gauges are always available so a minimal deployment still has
	// basic liveness/load signals.
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintf(&builder, "# TYPE go_goroutines gauge\n")
	fmt.Fprintf(&builder, "go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(&builder, "# TYPE go_memstats_alloc_bytes gauge\n")
	fmt.Fprintf(&builder, "go_memstats_alloc_bytes %d\n", mem.Alloc)
	fmt.Fprintf(&builder, "# TYPE process_uptime_seconds gauge\n")
	fmt.Fprintf(&builder, "process_uptime_seconds %s\n", formatFloat(time.Since(r.startedAt).Seconds()))
	return builder.String()
}

func sortedKeys(series map[string]float64) []string {
	keys := make([]string, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysHistogram(series map[string]*histogram) []string {
	keys := make([]string, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Handler serves the rendered metrics with the Prometheus content type.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(r.Render()))
	})
}

// Handler serves the default registry.
func Handler() http.Handler { return defaultRegistry.Handler() }

// Instrumentation helpers for the research assistant domain.

// RecordHTTPRequest records one HTTP request.
func RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	labels := Labels{"method": method, "path": path, "status": strconv.Itoa(status)}
	Default().Inc("http_requests_total", labels)
	Default().Observe("http_request_duration_seconds", duration.Seconds(), Labels{"method": method, "path": path})
}

// RecordPlanCreated records a newly created plan.
func RecordPlanCreated(intentType string) {
	Default().Inc("plan_created_total", Labels{"intent_type": intentType})
}

// RecordPlanTerminal records a terminal plan and its duration.
func RecordPlanTerminal(status, outcome string, duration time.Duration) {
	labels := Labels{"status": status, "outcome": outcome}
	Default().Inc("plan_terminal_total", labels)
	Default().Observe("plan_duration_seconds", duration.Seconds(), Labels{"outcome": outcome})
}

// RecordTask records one task result and duration.
func RecordTask(taskType, status string, duration time.Duration) {
	Default().Inc("task_total", Labels{"type": taskType, "status": status})
	Default().Observe("task_duration_seconds", duration.Seconds(), Labels{"type": taskType})
	if status != "completed" {
		Default().Inc("task_failure_total", Labels{"type": taskType})
	}
}

// RecordLLMCall records one model invocation.
func RecordLLMCall(model string, promptTokens, completionTokens int, latency time.Duration, estimated bool) {
	labels := Labels{"model": model}
	Default().Inc("llm_calls_total", labels)
	Default().Add("llm_tokens_total", float64(promptTokens), Labels{"model": model, "kind": "prompt"})
	Default().Add("llm_tokens_total", float64(completionTokens), Labels{"model": model, "kind": "completion"})
	Default().Observe("llm_latency_seconds", latency.Seconds(), labels)
	if estimated {
		Default().Inc("llm_estimated_usage_total", labels)
	}
}

// RecordSandboxExecution records a sandbox run duration.
func RecordSandboxExecution(duration time.Duration, ok bool) {
	Default().Observe("sandbox_execution_seconds", duration.Seconds(), nil)
	if !ok {
		Default().Inc("sandbox_errors_total", nil)
	}
}

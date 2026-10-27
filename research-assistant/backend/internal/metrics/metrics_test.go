package metrics

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestCounterGaugeAndHistogramRender(t *testing.T) {
	registry := NewRegistry()
	registry.Describe("demo_requests_total", "counter", "demo counter")
	registry.Inc("demo_requests_total", Labels{"status": "200"})
	registry.Inc("demo_requests_total", Labels{"status": "200"})
	registry.Inc("demo_requests_total", Labels{"status": "500"})
	registry.Set("demo_inflight", 3, nil)
	registry.Observe("demo_latency_seconds", 0.2, nil)
	registry.Observe("demo_latency_seconds", 1.5, nil)

	if got := registry.CounterValue("demo_requests_total", Labels{"status": "200"}); got != 2 {
		t.Fatalf("counter=%v", got)
	}
	if got := registry.GaugeValue("demo_inflight", nil); got != 3 {
		t.Fatalf("gauge=%v", got)
	}
	if got := registry.HistogramCount("demo_latency_seconds", nil); got != 2 {
		t.Fatalf("histogram count=%d", got)
	}
	rendered := registry.Render()
	for _, want := range []string{
		"# TYPE demo_requests_total counter",
		`demo_requests_total{status="200"} 2`,
		"demo_inflight 3",
		"demo_latency_seconds_bucket{le=\"+Inf\"} 2",
		"demo_latency_seconds_sum",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestHistogramBucketsAreCumulative(t *testing.T) {
	registry := NewRegistry()
	registry.SetBuckets("latency", []float64{1, 5, 10})
	registry.Observe("latency", 0.5, nil)
	registry.Observe("latency", 5, nil)
	registry.Observe("latency", 10, nil)
	rendered := registry.Render()
	for _, want := range []string{
		`latency_bucket{le="1"} 1`,
		`latency_bucket{le="5"} 2`,
		`latency_bucket{le="10"} 3`,
		`latency_bucket{le="+Inf"} 3`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestDomainHelpers(t *testing.T) {
	registry := Default()
	registry.Reset()
	RecordHTTPRequest("GET", "/api/plans", 200, 120*time.Millisecond)
	RecordPlanCreated("Code_Execution")
	RecordPlanTerminal("completed", "completed", 3*time.Second)
	RecordTask("execute_code", "failed", 2*time.Second)
	RecordLLMCall("deepseek-test", 100, 50, 900*time.Millisecond, true)
	RecordSandboxExecution(4*time.Second, false)

	if got := registry.CounterValue("http_requests_total", Labels{"method": "GET", "path": "/api/plans", "status": "200"}); got != 1 {
		t.Fatalf("http counter=%v", got)
	}
	if got := registry.CounterValue("llm_tokens_total", Labels{"model": "deepseek-test", "kind": "prompt"}); got != 100 {
		t.Fatalf("prompt tokens=%v", got)
	}
	if got := registry.CounterValue("sandbox_errors_total", nil); got != 1 {
		t.Fatalf("sandbox errors=%v", got)
	}
	if count := registry.HistogramCount("llm_latency_seconds", Labels{"model": "deepseek-test"}); count != 1 {
		t.Fatalf("llm histogram=%d", count)
	}
}

func TestFormatFloat(t *testing.T) {
	cases := map[float64]string{0: "0", 1: "1", 1.5: "1.5", 0.25: "0.25", math.Inf(1): "+Inf"}
	for value, want := range cases {
		if got := formatFloat(value); got != want {
			t.Fatalf("formatFloat(%v)=%q want %q", value, got, want)
		}
	}
}

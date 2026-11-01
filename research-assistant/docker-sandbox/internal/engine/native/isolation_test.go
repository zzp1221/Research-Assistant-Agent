package native

import (
	"strings"
	"testing"
)

func TestEvaluateProbe(t *testing.T) {
	report := EvaluateProbe([]ProbeCase{
		{Name: "multiprocessing", Passed: true},
		{Name: "network_connect", Security: true, Passed: true},
	})
	if !report.AppContainerOK || report.Reason != "" {
		t.Fatalf("report = %#v", report)
	}
	failed := EvaluateProbe([]ProbeCase{
		{Name: "multiprocessing", Passed: false},
		{Name: "network_connect", Security: true, Passed: false},
	})
	if failed.AppContainerOK {
		t.Fatal("failed probe must not allow appcontainer")
	}
	if !strings.Contains(failed.Reason, "multiprocessing") || !strings.Contains(failed.Reason, "network_connect") {
		t.Fatalf("reason = %q", failed.Reason)
	}
	if !strings.Contains(failed.Reason, "security") {
		t.Fatalf("reason should mark security failure: %q", failed.Reason)
	}
}

func TestSelectIsolation(t *testing.T) {
	pass := &ProbeReport{AppContainerOK: true}
	fail := &ProbeReport{AppContainerOK: false}
	cases := []struct {
		requested string
		report    *ProbeReport
		gpu       bool
		want      string
	}{
		{"", pass, false, IsolationAppContainer},
		{"auto", pass, false, IsolationAppContainer},
		{"auto", fail, false, IsolationLowIL},
		{"auto", pass, true, IsolationLowIL},
		{"appcontainer", fail, false, IsolationAppContainer},
		{"lowil", pass, false, IsolationLowIL},
		{"unknown", nil, false, IsolationLowIL},
	}
	for _, item := range cases {
		if got := SelectIsolation(item.requested, item.report, item.gpu); got != item.want {
			t.Fatalf("SelectIsolation(%q,%v,%v) = %q, want %q", item.requested, item.report, item.gpu, got, item.want)
		}
	}
}

func TestProbeDefinitions(t *testing.T) {
	definitions := ProbeDefinitions()
	if len(definitions) != 7 {
		t.Fatalf("len = %d, want 7", len(definitions))
	}
	seen := map[string]bool{}
	security := 0
	for _, definition := range definitions {
		if definition.Name == "" || definition.Python == "" {
			t.Fatalf("incomplete definition: %#v", definition)
		}
		if seen[definition.Name] {
			t.Fatalf("duplicate name %q", definition.Name)
		}
		seen[definition.Name] = true
		if definition.Security {
			security++
		}
	}
	if security != 3 {
		t.Fatalf("security cases = %d, want 3", security)
	}
	script := ProbeScript(".")
	if !strings.Contains(script, "multiprocessing") || !strings.Contains(script, "json.dumps") {
		t.Fatalf("script looks wrong: %s", script)
	}
}
func TestCPUPercent(t *testing.T) {
	if got := cpuPercent(0); got != 0 {
		t.Fatalf("cpuPercent(0) = %d", got)
	}
	if got := cpuPercent(1); got < 1 || got > 100 {
		t.Fatalf("cpuPercent(1) = %d", got)
	}
	if got := cpuPercent(1000); got != 100 {
		t.Fatalf("cpuPercent(1000) = %d", got)
	}
}

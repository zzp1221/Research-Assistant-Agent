package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func successfulProbe() ProbeReport {
	var cases []ProbeCase
	for _, d := range ProbeDefinitions() {
		cases = append(cases, newProbeCase(d, "pass", "verified"))
	}
	r := EvaluateProbe(cases)
	r.EnvironmentKey = "test-environment"
	return r
}

func TestEvaluateProbeRequiresCompleteUnambiguousEvidence(t *testing.T) {
	good := successfulProbe()
	if !good.AppContainerOK || good.Passed != len(ProbeDefinitions()) {
		t.Fatalf("complete report rejected: %#v", good)
	}
	for _, status := range []string{"fail", "error", "inconclusive", "skipped", "unknown"} {
		t.Run(status, func(t *testing.T) {
			cases := append([]ProbeCase(nil), good.Cases...)
			cases[0].Status, cases[0].Passed = status, false
			if EvaluateProbe(cases).AppContainerOK {
				t.Fatal("non-pass status established isolation")
			}
		})
	}
	malformed := [][]ProbeCase{nil, good.Cases[:len(good.Cases)-1], append(append([]ProbeCase(nil), good.Cases...), good.Cases[0])}
	for _, cases := range malformed {
		if EvaluateProbe(cases).AppContainerOK {
			t.Fatal("missing/duplicate evidence accepted")
		}
	}
	for _, mutation := range []func(*ProbeCase){
		func(c *ProbeCase) { c.Passed = false }, func(c *ProbeCase) { c.Profile = "host" },
		func(c *ProbeCase) { c.Expected = "anything" }, func(c *ProbeCase) { c.Name = "unknown" },
	} {
		cases := append([]ProbeCase(nil), good.Cases...)
		mutation(&cases[0])
		if EvaluateProbe(cases).AppContainerOK {
			t.Fatal("invalid evidence accepted")
		}
	}
}

func TestProbeDenialCannotBeEstablishedByNetworkFailure(t *testing.T) {
	d := ProbeDefinition{Name: "network_connect", Security: true, Expected: "deny", Profile: "offline"}
	for _, winerror := range []int{0, 10060, 10061, 11001} {
		if c := evaluateObservation(d, probeObservation{Operation: d.Name, Outcome: "error", Winerror: winerror}); c.Status != "inconclusive" {
			t.Fatalf("environment failure passed: %#v", c)
		}
	}
	if c := evaluateObservation(d, probeObservation{Operation: d.Name, Outcome: "denied", Winerror: 10013}); !c.Passed {
		t.Fatalf("explicit OS denial rejected: %#v", c)
	}
	if c := evaluateObservation(d, probeObservation{Operation: d.Name, Outcome: "success"}); c.Status != "fail" {
		t.Fatal("forbidden operation success must fail")
	}
}

func TestProbeObservationRejectsAmbiguousAndUnknownOutput(t *testing.T) {
	for _, output := range []string{"", `{"operation":"wrong","outcome":"success"}`, `{"operation":"x","outcome":"passed"}`, "{}\n{}"} {
		if _, err := parseProbeObservation(output, "x"); err == nil {
			t.Fatal("invalid output accepted")
		}
	}
}

func TestResolveIsolationHonorsVerifiedEvidenceAndExplicitRequests(t *testing.T) {
	good := successfulProbe()
	legacy := good
	legacy.Version = "v1"
	bad := cloneProbeReport(good)
	bad.Cases[4] = newProbeCase(ProbeDefinitions()[4], "fail", "escape")
	for _, tc := range []struct {
		request string
		report  *ProbeReport
		gpu     bool
		want    string
		blocked bool
	}{
		{"auto", &good, false, IsolationAppContainer, false},
		{"appcontainer", &good, false, IsolationAppContainer, false},
		{"appcontainer", nil, false, "", true},
		{"appcontainer", &legacy, false, "", true},
		{"auto", &bad, false, "", true},
		{"auto", nil, false, IsolationLowIL, false},
		{"lowil", &bad, false, IsolationLowIL, false},
		{"auto", &good, true, IsolationLowIL, false},
	} {
		got, err := ResolveIsolation(tc.request, tc.report, tc.gpu)
		if got != tc.want || (err != nil) != tc.blocked {
			t.Fatalf("ResolveIsolation(%s): %s %v", tc.request, got, err)
		}
	}
}

func TestProbeFingerprintChangesForEnvironmentAndPolicy(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python.exe")
	if err := os.WriteFile(python, []byte("interpreter"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ProbeEnvironmentKey(python, "env", "offline")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(){
		func() { _ = os.WriteFile(filepath.Join(root, "pyvenv.cfg"), []byte("new version"), 0o600) },
		func() { _ = os.WriteFile(python, []byte("new interpreter"), 0o600) },
		func() {
			_ = os.MkdirAll(filepath.Join(root, "site-packages"), 0o700)
			_ = os.WriteFile(filepath.Join(root, "site-packages", "module.py"), []byte("new dependency"), 0o600)
		},
	} {
		mutate()
		next, err := ProbeEnvironmentKey(python, "env", "offline")
		if err != nil {
			t.Fatal(err)
		}
		if next == key {
			t.Fatal("stale environment fingerprint")
		}
		key = next
	}
	next, err := ProbeEnvironmentKey(python, "env", "online")
	if err != nil || next == key {
		t.Fatal("configuration change not fingerprinted")
	}
}

func TestProbeScriptDoesNotSelfDeclarePassedOrInstallPackages(t *testing.T) {
	for _, d := range ProbeDefinitions() {
		script := ProbeScript(d.Name, "target")
		if strings.Contains(script, "'passed'") || strings.Contains(script, "pip','install") || strings.Contains(script, "1.1.1.1") {
			t.Fatal("unsafe or self-grading script")
		}
	}
}

func TestCPUPercent(t *testing.T) {
	if cpuPercent(0) != 0 || cpuPercent(1) < 1 || cpuPercent(1000) != 100 {
		t.Fatal("invalid CPU limits")
	}
}

package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"docker-sandbox/internal/engine"
)

type probeBoundaryRunner struct {
	probe *ProbeReport
	runs  int
}

func (r *probeBoundaryRunner) SetProbe(p *ProbeReport) { r.probe = p }
func (r *probeBoundaryRunner) Close() error            { return nil }
func (r *probeBoundaryRunner) Run(context.Context, runSpec) (*engine.Result, error) {
	r.runs++
	return &engine.Result{}, nil
}

func probeCacheFixture(t *testing.T) (*Provider, string, *probeBoundaryRunner) {
	t.Helper()
	root := t.TempDir()
	python := filepath.Join(root, "runtime", "python.exe")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &probeBoundaryRunner{}
	p := &Provider{cfg: Config{DataDir: root, Runner: IsolationAuto}, runner: runner,
		sessions: map[string]*nativeSession{},
		probeRun: func(context.Context, string, string) ProbeReport { return successfulProbe() }}
	return p, python, runner
}

func TestNativeProbeRevalidatesBeforeExecutingExistingSession(t *testing.T) {
	p, python, runner := probeCacheFixture(t)
	if !p.probe(t.Context(), python).AppContainerOK {
		t.Fatal("valid fixture report rejected")
	}
	p.sessions["session"] = &nativeSession{id: "session", workspace: t.TempDir(), venvPython: python, isolation: IsolationAppContainer}
	// Simulate an OS policy change that leaves runtime file contents unchanged.
	observations := 0
	p.probeRun = func(context.Context, string, string) ProbeReport {
		observations++
		cases := successfulProbe().Cases
		cases[4] = newProbeCase(ProbeDefinitions()[4], "fail", "policy changed")
		return EvaluateProbe(cases)
	}
	if _, err := p.Exec(t.Context(), "session", engine.Command{Argv: []string{python}}, nil); err == nil {
		t.Fatal("old passing report authorized execution after a policy change")
	}
	if observations != 1 || runner.runs != 0 {
		t.Fatal("execution was not blocked by fresh evidence")
	}
}

func TestNativeProbeRejectsRuntimeMutationDuringProbe(t *testing.T) {
	p, python, _ := probeCacheFixture(t)
	p.probeRun = func(context.Context, string, string) ProbeReport {
		if err := os.WriteFile(python, []byte("changed during probe"), 0o600); err != nil {
			t.Fatal(err)
		}
		return successfulProbe()
	}
	r := p.probe(t.Context(), python)
	if r.AppContainerOK {
		t.Fatal("report authorized a runtime changed during observation")
	}
	if _, err := ResolveIsolation(IsolationAppContainer, &r, false); err == nil {
		t.Fatal("changed environment did not block")
	}
}

func TestNativeCapabilitiesInvalidateChangedAndExpiredProbe(t *testing.T) {
	for _, change := range []string{"runtime", "policy", "expired"} {
		t.Run(change, func(t *testing.T) {
			p, python, runner := probeCacheFixture(t)
			r := p.probe(t.Context(), python)
			r.Cases[0].Status = "fail"
			caps, err := p.Capabilities(t.Context())
			if err != nil || caps.Isolation != IsolationAppContainer {
				t.Fatal("report snapshot was aliased or not verified")
			}
			switch change {
			case "runtime":
				if err := os.WriteFile(python, []byte("new runtime"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "policy":
				t.Setenv("SANDBOX_PROBE_NETWORK_ADDRESS", "new-policy.example:443")
			case "expired":
				p.probeCheckedAt = time.Now().Add(-probeSnapshotMaxAge)
			}
			caps, err = p.Capabilities(t.Context())
			if err != nil || caps.NetworkControl || caps.Isolation == IsolationAppContainer || caps.Probe == nil || caps.Probe.AppContainerOK || runner.probe != nil {
				t.Fatal("stale probe still advertised or authorized strong isolation")
			}
		})
	}
}

func TestNativeCleanupInvalidatesProbeAndRunner(t *testing.T) {
	p, python, runner := probeCacheFixture(t)
	p.runtimes = NewRuntimeManager("unused", filepath.Dir(python), filepath.Join(p.cfg.DataDir, "venvs"), filepath.Join(p.cfg.DataDir, "logs"))
	p.probe(t.Context(), python)
	if _, err := p.CleanupRuntimeCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.probeReport != nil || p.probePython != "" || runner.probe != nil {
		t.Fatal("cleanup retained a probe for deleted runtime")
	}
}

func TestProbeFingerprintIncludesVenvBaseRuntime(t *testing.T) {
	root := t.TempDir()
	base, venv := filepath.Join(root, "base"), filepath.Join(root, "venv")
	for _, dir := range []string{base, filepath.Join(venv, "Scripts")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(venv, "Scripts", "python.exe")
	if err := os.WriteFile(python, []byte("redirector"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = "+base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseDLL := filepath.Join(base, "python312.dll")
	if err := os.WriteFile(baseDLL, []byte("old base"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ProbeEnvironmentKey(python, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseDLL, []byte("new base"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := ProbeEnvironmentKey(python, "", "")
	if err != nil || before == after {
		t.Fatal("base Python changed without invalidating venv report")
	}
}

func TestProbeSnapshotRejectsMissingFutureAndExpiredTimestamps(t *testing.T) {
	now := time.Now()
	for _, stamp := range []time.Time{{}, now.Add(time.Second), now.Add(-probeSnapshotMaxAge)} {
		if probeSnapshotFresh(stamp, now) {
			t.Fatal("invalid freshness evidence accepted")
		}
	}
}

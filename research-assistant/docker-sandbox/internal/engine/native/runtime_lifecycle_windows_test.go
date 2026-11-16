//go:build windows

package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"docker-sandbox/internal/engine"
)

func createLifecycleFixture(t *testing.T, requested string, verified bool) (*Provider, string) {
	t.Helper()
	p, err := NewProvider(Config{DataDir: t.TempDir(), Runner: requested})
	if err != nil {
		t.Fatal(err)
	}
	hash := EnvHash("3.11", nil, p.runtimes.platform())
	venv := filepath.Join(p.runtimes.VenvRoot, hash[:16])
	base := filepath.Join(p.runtimes.RuntimeRoot, "base")
	for _, path := range []string{filepath.Join(venv, "Scripts"), base} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := pythonExecutable(venv)
	if err := os.WriteFile(python, []byte("synthetic-lifecycle-interpreter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = "+base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This fixture exercises lifecycle decisions only. Real token and Python
	// compatibility evidence is covered by the independent Windows probes.
	p.probeRun = func(context.Context, string, string) ProbeReport {
		if verified {
			return successfulProbe()
		}
		return EvaluateProbe(nil)
	}
	return p, python
}

func TestNativeCreateReportsSelectedCopyVenvAndDeleteCleansIt(t *testing.T) {
	p, source := createLifecycleFixture(t, IsolationAppContainer, true)
	session, err := p.Create(t.Context(), engine.CreateSpec{PythonVer: "3.11", NetworkMode: engine.NetworkNone})
	if err != nil {
		t.Fatal(err)
	}
	internal, err := p.session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if internal.venvPython == source || session.VenvPath != filepath.Dir(filepath.Dir(internal.venvPython)) {
		t.Fatal("session metadata does not describe its selected runtime copy")
	}
	if _, err := os.Stat(internal.venvPython); err != nil {
		t.Fatal("selected interpreter copy is missing")
	}
	if err := p.Delete(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(internal.runtimeCopyRoot); !os.IsNotExist(err) {
		t.Fatal("Delete left the created runtime copy behind")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("Delete changed the source runtime")
	}
}

func TestNativeCreateFailureAndFallbackDiscardUnusedCopies(t *testing.T) {
	for _, requested := range []string{IsolationAppContainer, IsolationAuto} {
		t.Run(requested, func(t *testing.T) {
			p, source := createLifecycleFixture(t, requested, false)
			session, err := p.Create(t.Context(), engine.CreateSpec{PythonVer: "3.11", NetworkMode: engine.NetworkBridge})
			if requested == IsolationAppContainer {
				if err == nil || len(p.sessions) != 0 {
					t.Fatal("failed strong-isolation create retained a session")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				internal, _ := p.session(session.ID)
				if internal.venvPython != source || internal.runtimeCopyRoot != "" || session.Isolation != IsolationLowIL || session.VenvPath != filepath.Dir(filepath.Dir(source)) {
					t.Fatal("fallback used an AppContainer copy or incorrect venv metadata")
				}
			}
			entries, readErr := os.ReadDir(p.appContainerRuntimeRoot())
			if readErr != nil || len(entries) != 0 {
				t.Fatal("failed or fallback create leaked an unused runtime copy")
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal("discarding an unused copy changed the source runtime")
			}
		})
	}
}

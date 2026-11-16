package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"docker-sandbox/internal/engine"
)

func lifecycleFixture(t *testing.T) (*Provider, string, string) {
	t.Helper()
	root := t.TempDir()
	p := &Provider{cfg: Config{DataDir: root}, sessions: map[string]*nativeSession{},
		runtimes: NewRuntimeManager("unused", filepath.Join(root, "python"), filepath.Join(root, "venvs"), filepath.Join(root, "logs"))}
	source := filepath.Join(p.runtimes.RuntimeRoot, "source.exe")
	copyRoot := filepath.Join(p.appContainerRuntimeRoot(), "session")
	for _, file := range []string{source, filepath.Join(copyRoot, "copy.exe")} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("lifecycle-fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p.sessions["session"] = &nativeSession{id: "session", runtimeCopyRoot: copyRoot, createdAt: time.Now().Add(-time.Hour)}
	return p, source, copyRoot
}

func TestNativeDeleteAndReapRemoveOnlySessionCopies(t *testing.T) {
	for _, operation := range []string{"delete", "reap"} {
		t.Run(operation, func(t *testing.T) {
			p, source, copyRoot := lifecycleFixture(t)
			p.probePython = filepath.Join(copyRoot, "copy.exe")
			probe := successfulProbe()
			p.probeReport, p.probeCheckedAt = &probe, time.Now()
			if operation == "delete" {
				if err := p.Delete(t.Context(), "session"); err != nil {
					t.Fatal(err)
				}
			} else if count, err := p.Reap(t.Context(), time.Minute); err != nil || count != 1 {
				t.Fatalf("reap count=%d err=%v", count, err)
			}
			if _, err := os.Stat(copyRoot); !os.IsNotExist(err) {
				t.Fatal("session runtime copy survived removal")
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal("removing a session changed its shared source runtime")
			}
			if _, err := p.session("session"); err == nil || p.probeReport != nil || p.probePython != "" {
				t.Fatal("removed session retained execution or probe state")
			}
		})
	}
}

func TestNativeRemovalFailureRetainsRetryAndUnlocks(t *testing.T) {
	for _, operation := range []string{"delete", "reap"} {
		t.Run(operation, func(t *testing.T) {
			p, source, copyRoot := lifecycleFixture(t)
			p.sessions["session"].runtimeCopyRoot = filepath.Dir(source)
			var err error
			if operation == "delete" {
				err = p.Delete(t.Context(), "session")
			} else {
				_, err = p.Reap(t.Context(), time.Minute)
			}
			if err == nil {
				t.Fatal("cleanup accepted a target outside the copy directory")
			}
			unlocked := make(chan struct{})
			go func() {
				p.mu.Lock()
				if p.sessions["session"] != nil {
					p.sessions["session"].runtimeCopyRoot = copyRoot
				}
				p.mu.Unlock()
				close(unlocked)
			}()
			select {
			case <-unlocked:
			case <-time.After(time.Second):
				t.Fatal("failed session cleanup leaked the provider lock")
			}
			if _, err := p.session("session"); err != nil {
				t.Fatal("failed cleanup lost the session needed for retry")
			}
			if err := p.Delete(t.Context(), "session"); err != nil {
				t.Fatal("cleanup retry failed: ", err)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal("rejected cleanup deleted the shared source")
			}
		})
	}
}

func TestNativeCacheCleanupRequiresNoActiveSessions(t *testing.T) {
	p, source, copyRoot := lifecycleFixture(t)
	if _, err := p.CleanupRuntimeCache(t.Context()); err == nil {
		t.Fatal("cache cleanup deleted runtimes referenced by an active session")
	}
	for _, path := range []string{source, copyRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("rejected cleanup changed an active runtime")
		}
	}
	if err := p.Delete(t.Context(), "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CleanupRuntimeCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p.runtimes.RuntimeRoot, p.runtimes.VenvRoot, p.appContainerRuntimeRoot()} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("cache cleanup did not clear source and copy roots")
		}
	}
}

type lifecycleBlockingRunner struct{ started, release chan struct{} }

func (*lifecycleBlockingRunner) SetProbe(*ProbeReport) {}
func (*lifecycleBlockingRunner) Close() error          { return nil }
func (r *lifecycleBlockingRunner) Run(context.Context, runSpec) (*engine.Result, error) {
	close(r.started)
	<-r.release
	return &engine.Result{}, nil
}

func TestNativeDeleteWaitsForActiveRuntimeExecution(t *testing.T) {
	p, source, copyRoot := lifecycleFixture(t)
	runner := &lifecycleBlockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	p.runner = runner
	p.sessions["session"].workspace = t.TempDir()
	p.sessions["session"].venvPython = filepath.Join(copyRoot, "copy.exe")
	p.sessions["session"].isolation = IsolationLowIL
	executed := make(chan error, 1)
	go func() {
		_, err := p.Exec(t.Context(), "session", engine.Command{Argv: []string{source}}, nil)
		executed <- err
	}()
	<-runner.started
	deleted := make(chan error, 1)
	go func() { deleted <- p.Delete(t.Context(), "session") }()
	select {
	case err := <-deleted:
		t.Fatalf("session runtime was removed while executing: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(runner.release)
	if err := <-executed; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
}

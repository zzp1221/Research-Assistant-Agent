package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeProcess struct {
	name     string
	mu       sync.Mutex
	started  bool
	killed   bool
	wait     chan struct{}
	events   *[]string
	eventsMu *sync.Mutex
}

func (p *fakeProcess) Start() error {
	p.mu.Lock()
	p.started = true
	p.mu.Unlock()
	return nil
}
func (p *fakeProcess) Wait() error {
	<-p.wait
	return nil
}
func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	if !p.killed {
		p.killed = true
		close(p.wait)
	}
	p.mu.Unlock()
	if p.events != nil {
		p.eventsMu.Lock()
		*p.events = append(*p.events, p.name)
		p.eventsMu.Unlock()
	}
	return nil
}
func (p *fakeProcess) Pid() int { return 1 }

type fakeRunner struct {
	mu        sync.Mutex
	processes map[string]*fakeProcess
	order     *[]string
	orderMu   *sync.Mutex
}

func (r *fakeRunner) Start(spec CommandSpec) (Process, error) {
	p := &fakeProcess{name: spec.Path, wait: make(chan struct{}), events: r.order, eventsMu: r.orderMu}
	r.mu.Lock()
	r.processes[spec.Path] = p
	r.mu.Unlock()
	return p, nil
}

func healthServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStartAndStopOrder(t *testing.T) {
	backendHealth := healthServer(t)
	sandboxHealth := healthServer(t)
	order := []string{}
	orderMu := &sync.Mutex{}
	runner := &fakeRunner{processes: map[string]*fakeProcess{}, order: &order, orderMu: orderMu}
	sup := New(Config{
		Backend:       CommandSpec{Path: "backend"},
		Sandbox:       CommandSpec{Path: "sandbox"},
		BackendHealth: backendHealth.URL,
		SandboxHealth: sandboxHealth.URL,
		HealthTimeout: time.Second,
		LockPath:      t.TempDir() + "/lock",
	}, runner)
	if err := sup.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !runner.processes["backend"].started || !runner.processes["sandbox"].started {
		t.Fatalf("processes were not started")
	}
	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) != 2 || order[0] != "sandbox" || order[1] != "backend" {
		t.Fatalf("stop order = %#v, want sandbox then backend", order)
	}
}

func TestStartFailureKillsBackend(t *testing.T) {
	backendHealth := healthServer(t)
	runner := &fakeRunner{processes: map[string]*fakeProcess{}, order: &[]string{}, orderMu: &sync.Mutex{}}
	sup := New(Config{
		Backend:       CommandSpec{Path: "backend"},
		Sandbox:       CommandSpec{Path: "sandbox"},
		BackendHealth: backendHealth.URL,
		SandboxHealth: "http://127.0.0.1:1/health",
		HealthTimeout: 100 * time.Millisecond,
		LockPath:      t.TempDir() + "/lock",
	}, runner)
	if err := sup.Start(context.Background()); err == nil {
		t.Fatalf("Start should fail when sandbox is unhealthy")
	}
	if !runner.processes["backend"].killed || !runner.processes["sandbox"].killed {
		t.Fatalf("both processes should be killed after failure")
	}
}

func TestPickPort(t *testing.T) {
	port, err := PickPort()
	if err != nil {
		t.Fatal(err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port = %d", port)
	}
}

func TestInstanceLockExcludesSecondInstance(t *testing.T) {
	path := t.TempDir() + "/lock"
	first, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInstanceLock(path); err == nil {
		t.Fatalf("second lock should fail")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = second.Release()
}

func TestWaitHealthyTimesOut(t *testing.T) {
	err := WaitHealthy(context.Background(), "http://127.0.0.1:1/health", 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout")
	}
	if !stringsContains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func stringsContains(value, substr string) bool {
	return len(value) >= len(substr) && fmt.Sprintf("%s", value) != "" && (len(substr) == 0 || indexOf(value, substr) >= 0)
}

func indexOf(value, substr string) int {
	for i := 0; i+len(substr) <= len(value); i++ {
		if value[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

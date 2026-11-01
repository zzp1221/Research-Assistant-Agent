// Package supervisor starts and stops the backend and sandbox child processes.
package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CommandSpec describes one child process.
type CommandSpec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// Process is a started child process.
type Process interface {
	Start() error
	Wait() error
	Kill() error
	Pid() int
}

// Runner starts child processes; tests inject fake runners.
type Runner interface {
	Start(spec CommandSpec) (Process, error)
}

// Config is the supervisor configuration.
type Config struct {
	Backend       CommandSpec
	Sandbox       CommandSpec
	BackendHealth string
	SandboxHealth string
	HealthTimeout time.Duration
	LockPath      string
}

// Supervisor owns the child process lifecycle.
type Supervisor struct {
	cfg     Config
	runner  Runner
	mu      sync.Mutex
	lock    *InstanceLock
	backend Process
	sandbox Process
}

func New(cfg Config, runner Runner) *Supervisor {
	if cfg.HealthTimeout <= 0 {
		cfg.HealthTimeout = 30 * time.Second
	}
	if cfg.LockPath == "" {
		cfg.LockPath = filepath.Join(os.TempDir(), "research-assistant-desktop.lock")
	}
	return &Supervisor{cfg: cfg, runner: runner}
}

// Start acquires the single-instance lock, starts backend then sandbox, and
// waits for each health endpoint. On failure it stops whatever it started.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil {
		return fmt.Errorf("supervisor is already started")
	}
	lock, err := AcquireInstanceLock(s.cfg.LockPath)
	if err != nil {
		return err
	}
	s.lock = lock

	backend, err := s.runner.Start(s.cfg.Backend)
	if err != nil {
		s.releaseLock()
		return fmt.Errorf("start backend: %w", err)
	}
	if err := backend.Start(); err != nil {
		s.releaseLock()
		return fmt.Errorf("start backend: %w", err)
	}
	s.backend = backend
	if err := WaitHealthy(ctx, s.cfg.BackendHealth, s.cfg.HealthTimeout); err != nil {
		s.stopProcesses()
		return fmt.Errorf("backend health check failed: %w", err)
	}

	sandbox, err := s.runner.Start(s.cfg.Sandbox)
	if err != nil {
		s.stopProcesses()
		return fmt.Errorf("start sandbox: %w", err)
	}
	if err := sandbox.Start(); err != nil {
		s.stopProcesses()
		return fmt.Errorf("start sandbox: %w", err)
	}
	s.sandbox = sandbox
	if err := WaitHealthy(ctx, s.cfg.SandboxHealth, s.cfg.HealthTimeout); err != nil {
		s.stopProcesses()
		return fmt.Errorf("sandbox health check failed: %w", err)
	}
	return nil
}

// Stop terminates sandbox first, then backend, and releases the lock.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	if s.sandbox != nil {
		if err := stopProcess(ctx, s.sandbox); err != nil && firstErr == nil {
			firstErr = err
		}
		s.sandbox = nil
	}
	if s.backend != nil {
		if err := stopProcess(ctx, s.backend); err != nil && firstErr == nil {
			firstErr = err
		}
		s.backend = nil
	}
	s.releaseLock()
	return firstErr
}

func (s *Supervisor) stopProcesses() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s.sandbox != nil {
		_ = stopProcess(ctx, s.sandbox)
		s.sandbox = nil
	}
	if s.backend != nil {
		_ = stopProcess(ctx, s.backend)
		s.backend = nil
	}
	s.releaseLock()
}

func (s *Supervisor) releaseLock() {
	if s.lock != nil {
		_ = s.lock.Release()
		s.lock = nil
	}
}

func stopProcess(ctx context.Context, process Process) error {
	if process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	if err := process.Kill(); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PickPort asks the OS for a free loopback port and returns it.
func PickPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", listener.Addr())
	}
	return addr.Port, nil
}

// WaitHealthy polls url until it returns a 2xx response or the timeout expires.
func WaitHealthy(ctx context.Context, url string, timeout time.Duration) error {
	if url == "" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health returned %s", resp.Status)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check timed out: %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

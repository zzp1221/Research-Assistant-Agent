package native

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// CleanRuntimeCache removes cached runtimes and venvs, then recreates empty
// roots so the provider can install again.
func CleanRuntimeCache(runtimeRoot, venvRoot string, additionalRoots ...string) error {
	for _, root := range append([]string{venvRoot, runtimeRoot}, additionalRoots...) {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// CleanupRuntimeCache clears the cache unless an install is running.
func (p *Provider) CleanupRuntimeCache(_ context.Context) (RuntimeStatus, error) {
	p.installMu.Lock()
	if p.installing {
		p.installMu.Unlock()
		return RuntimeStatus{}, fmt.Errorf("runtime install is in progress")
	}
	p.installing = true
	p.installStage = "cleanup"
	p.installError = ""
	p.installMu.Unlock()
	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	p.mu.Lock()
	activeSessions := len(p.sessions)
	p.mu.Unlock()
	if activeSessions != 0 {
		p.installMu.Lock()
		p.installing = false
		p.installStage = "error"
		p.installError = "runtime cache is in use by active native sessions; delete the sessions before cleanup"
		p.installMu.Unlock()
		return RuntimeStatus{}, fmt.Errorf("runtime cache is in use by active native sessions; delete the sessions before cleanup")
	}
	// Even a partially failed cleanup invalidates the tested interpreter.
	p.probeMu.Lock()
	p.probeReport = nil
	p.probePython = ""
	p.probeCheckedAt = time.Time{}
	if p.runner != nil {
		p.runner.SetProbe(nil)
	}
	err := CleanRuntimeCache(p.runtimes.RuntimeRoot, p.runtimes.VenvRoot, p.appContainerRuntimeRoot())
	p.probeMu.Unlock()
	p.installMu.Lock()
	p.installing = false
	if err != nil {
		p.installStage = "error"
		p.installError = err.Error()
	} else {
		p.installStage = "done"
	}
	p.installMu.Unlock()
	if err != nil {
		return RuntimeStatus{}, err
	}
	return p.RuntimeStatus(context.Background())
}

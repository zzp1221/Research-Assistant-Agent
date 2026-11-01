package native

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// CleanRuntimeCache removes cached runtimes and venvs, then recreates empty
// roots so the provider can install again.
func CleanRuntimeCache(runtimeRoot, venvRoot string) error {
	for _, root := range []string{venvRoot, runtimeRoot} {
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
	err := CleanRuntimeCache(p.runtimes.RuntimeRoot, p.runtimes.VenvRoot)
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

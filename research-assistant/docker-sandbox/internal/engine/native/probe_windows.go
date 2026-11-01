//go:build windows

package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runProbe executes the appendix-D compatibility probe with the prepared
// Python interpreter and aggregates the results.
func runProbe(ctx context.Context, pythonPath, workDir string) ProbeReport {
	dir := filepath.Join(workDir, "probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ProbeReport{Reason: err.Error()}
	}
	scriptPath := filepath.Join(dir, "probe.py")
	if err := os.WriteFile(scriptPath, []byte(ProbeScript(dir)), 0o600); err != nil {
		return ProbeReport{Reason: err.Error()}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(probeCtx, pythonPath, scriptPath)
	command.Dir = dir
	output, err := command.CombinedOutput()
	cases := parseProbeOutput(string(output))
	if len(cases) == 0 {
		return ProbeReport{Reason: fmt.Sprintf("probe produced no results: %v (%s)", err, tailText(string(output), 2000))}
	}
	report := EvaluateProbe(completeProbeCases(cases))
	if err != nil {
		report.AppContainerOK = false
		if report.Reason == "" {
			report.Reason = fmt.Sprintf("probe exited with error: %v", err)
		}
	}
	return report
}

func parseProbeOutput(output string) []ProbeCase {
	cases := []ProbeCase{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var item ProbeCase
		if err := json.Unmarshal([]byte(line), &item); err == nil && item.Name != "" {
			cases = append(cases, item)
		}
	}
	return cases
}

func completeProbeCases(cases []ProbeCase) []ProbeCase {
	byName := map[string]ProbeCase{}
	for _, item := range cases {
		byName[item.Name] = item
	}
	out := make([]ProbeCase, 0, len(ProbeDefinitions()))
	for _, definition := range ProbeDefinitions() {
		item, ok := byName[definition.Name]
		if !ok {
			item = ProbeCase{Name: definition.Name, Security: definition.Security, Passed: false, Detail: "probe did not run"}
		}
		out = append(out, item)
	}
	return out
}

func tailText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

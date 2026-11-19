package native

import (
	"fmt"
	"strings"

	"docker-sandbox/internal/engine"
)

const (
	IsolationAuto         = "auto"
	IsolationAppContainer = "appcontainer"
	IsolationLowIL        = "lowil"
)

type ProbeCase = engine.ProbeCase
type ProbeReport = engine.ProbeReport

func EvaluateProbe(cases []ProbeCase) ProbeReport {
	r := ProbeReport{Version: ProbeVersion, Runner: IsolationAppContainer, Cases: append([]ProbeCase(nil), cases...)}
	required := map[string]ProbeDefinition{}
	for _, d := range ProbeDefinitions() {
		required[d.Name] = d
	}
	seen := map[string]bool{}
	var reasons []string
	for _, c := range cases {
		d, known := required[c.Name]
		if !known || seen[c.Name] || c.Expected != d.Expected || c.Profile != d.Profile || c.Security != d.Security || c.Passed != (c.Status == "pass") {
			reasons = append(reasons, "invalid or duplicate probe result: "+c.Name)
			r.Errors++
			continue
		}
		seen[c.Name] = true
		r.Attempted++
		switch c.Status {
		case "pass":
			r.Passed++
		case "fail":
			r.Failed++
		case "error":
			r.Errors++
		case "inconclusive":
			r.Inconclusive++
		default:
			r.Errors++
		}
		if c.Status != "pass" {
			reasons = append(reasons, c.Name+" ("+c.Status+")")
		}
	}
	for _, d := range ProbeDefinitions() {
		if !seen[d.Name] {
			reasons = append(reasons, "missing probe: "+d.Name)
			r.Errors++
		}
	}
	r.AppContainerOK = len(reasons) == 0 && r.Passed == len(required) && r.Errors == 0
	if !r.AppContainerOK {
		r.Reason = "isolation not verified: " + strings.Join(reasons, "; ")
	}
	return r
}

func verifiedProbe(r *ProbeReport) bool {
	return r != nil && r.Version == ProbeVersion && r.Runner == IsolationAppContainer &&
		r.EnvironmentKey != "" && r.AppContainerOK && EvaluateProbe(r.Cases).AppContainerOK
}

// A strong-isolation request needs a complete current report. A proven
// security failure also blocks auto instead of silently weakening isolation.
func ResolveIsolation(requested string, report *ProbeReport, gpuRequired bool) (string, error) {
	requested = NormalizeIsolation(requested)
	if requested == IsolationLowIL {
		return IsolationLowIL, nil
	}
	if verifiedProbe(report) && !gpuRequired {
		return IsolationAppContainer, nil
	}
	if requested == IsolationAppContainer {
		return "", fmt.Errorf("sandbox_capability: AppContainer isolation is not verified")
	}
	if report != nil {
		for _, c := range report.Cases {
			if c.Security && c.Status == "fail" {
				return "", fmt.Errorf("sandbox_capability: isolation security probe failed: %s", c.Name)
			}
		}
	}
	return IsolationLowIL, nil
}

func SelectIsolation(requested string, report *ProbeReport, gpuRequired bool) string {
	isolation, _ := ResolveIsolation(requested, report, gpuRequired)
	return isolation
}

func NormalizeIsolation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case IsolationAppContainer, IsolationLowIL:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return IsolationAuto
	}
}

package native

import (
	"fmt"
	"strings"

	"docker-sandbox/internal/engine"
)

// Isolation levels supported by the native provider.
const (
	IsolationAuto         = "auto"
	IsolationAppContainer = "appcontainer"
	IsolationLowIL        = "lowil"
)

// ProbeCase and ProbeReport are shared with the REST capability payload.
type ProbeCase = engine.ProbeCase
type ProbeReport = engine.ProbeReport

// EvaluateProbe marks AppContainer usable only when every probe case passes.
// Compatibility failures degrade to lowil; security failures are go/no-go.
func EvaluateProbe(cases []ProbeCase) ProbeReport {
	report := ProbeReport{Cases: cases, AppContainerOK: true}
	var failures []string
	for _, item := range cases {
		if item.Passed {
			continue
		}
		report.AppContainerOK = false
		kind := "compatibility"
		if item.Security {
			kind = "security"
		}
		failures = append(failures, fmt.Sprintf("%s (%s)", item.Name, kind))
	}
	if len(failures) > 0 {
		report.Reason = "probe failed: " + strings.Join(failures, ", ")
	}
	return report
}

// SelectIsolation applies the documented policy: an explicit setting wins,
// GPU tasks force lowil, and auto uses AppContainer only when the probe passed.
func SelectIsolation(requested string, report *ProbeReport, gpuRequired bool) string {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case IsolationAppContainer:
		return IsolationAppContainer
	case IsolationLowIL:
		return IsolationLowIL
	}
	if gpuRequired {
		return IsolationLowIL
	}
	if report != nil && report.AppContainerOK {
		return IsolationAppContainer
	}
	return IsolationLowIL
}

// NormalizeIsolation returns a known isolation value or auto.
func NormalizeIsolation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case IsolationAppContainer, IsolationLowIL:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return IsolationAuto
	}
}

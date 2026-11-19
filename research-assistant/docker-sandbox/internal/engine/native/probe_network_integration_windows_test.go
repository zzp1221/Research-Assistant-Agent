//go:build windows

package native

import (
	"context"
	"os"
	"testing"
	"time"
)

// Complements the test-owned loopback endpoint. The configured endpoint is
// contacted only by TCP connect/close; no application data or credentials.
// A timeout is never accepted as proof of OS denial.
func TestProbeWindowsConfiguredNetworkPositiveAndNegativeControl(t *testing.T) {
	endpoint := os.Getenv("RA_TEST_PROBE_NETWORK_ADDRESS")
	if endpoint == "" {
		t.Skip("set RA_TEST_PROBE_NETWORK_ADDRESS to an operator-selected reachable TCP endpoint")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	endpoint, err := probeControlledNetworkTarget(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	python := probePythonFixture(t)
	workspace := t.TempDir()
	if err := prepareAppContainerWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	for _, d := range ProbeDefinitions() {
		if d.Name != "network_connect" && d.Name != "network_online" {
			continue
		}
		output, err := runAppContainerProbe(ctx, python, workspace, d.Profile == "online", ProbeScript(d.Name, endpoint))
		if err != nil {
			t.Fatal(err)
		}
		observation, err := parseProbeObservation(output, d.Name)
		if err != nil {
			t.Fatal(err)
		}
		result := evaluateObservation(d, observation)
		t.Logf("actual profile=%s expected=%s status=%s winerror=%d detail=%s", d.Profile, d.Expected, result.Status, observation.Winerror, result.Detail)
		if !result.Passed {
			t.Errorf("configured network capability remains unverified: %#v", result)
		}
	}
}

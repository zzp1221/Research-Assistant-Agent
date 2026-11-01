//go:build !windows

package native

import "context"

// runProbe cannot run outside Windows.
func runProbe(context.Context, string, string) ProbeReport {
	return ProbeReport{Reason: "appcontainer probe requires windows"}
}

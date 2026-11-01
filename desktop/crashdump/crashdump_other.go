//go:build !windows

package crashdump

// Enable is a no-op outside Windows.
func Enable(string) error { return nil }

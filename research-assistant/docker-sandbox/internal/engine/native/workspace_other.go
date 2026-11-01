//go:build !windows

package native

// prepareAppContainerWorkspace is a no-op outside Windows because the native
// provider itself is unavailable there.
func prepareAppContainerWorkspace(string) error { return nil }

//go:build !windows

package native

// newCommandRunner always reports that native sandboxing is unavailable on
// non-Windows platforms.
func newCommandRunner(Config) (commandRunner, error) {
	return nil, ErrUnsupportedPlatform
}

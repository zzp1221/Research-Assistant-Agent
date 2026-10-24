//go:build windows

package engine

// diskFreeBytes is unavailable on the Windows build; the sandbox engine itself
// is only deployed on Linux. Returning 0 keeps the capability probe testable.
func diskFreeBytes(string) int64 {
	return 0
}

// Package winpath normalizes Windows long paths. On other platforms it is a
// no-op so callers can use it unconditionally.
package winpath

// LongPath returns an absolute path prefixed with the Windows extended-length
// prefix when the path may exceed MAX_PATH. Non-Windows platforms return the
// input unchanged.
func LongPath(path string) string {
	return longPath(path)
}

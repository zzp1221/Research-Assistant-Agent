//go:build !windows

package winpath

func longPath(path string) string { return path }

//go:build !windows

package native

import "path/filepath"

func canonicalEnvironmentPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func systemEnvironment() (map[string]string, error) {
	return map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"}, nil
}

func prepareEnvironmentDirectories(string, ...string) error { return nil }

//go:build !windows

package native

import "runtime"

func probePlatformKey() string { return runtime.GOOS }

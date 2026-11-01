package shell

import (
	"os/exec"
	"runtime"
)

// openLogDir opens dir in the platform file manager. It is best effort: the
// error page must never block on a missing desktop session.
func openLogDir(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

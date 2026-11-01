//go:build windows

package crashdump

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// Enable registers WER LocalDumps for the desktop processes. It is idempotent.
func Enable(dataDir string) error {
	dumpDir := DumpDir(dataDir)
	if err := os.MkdirAll(dumpDir, 0o700); err != nil {
		return fmt.Errorf("create crash dir: %w", err)
	}
	for _, target := range Targets(dataDir) {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, target.RegistryPath, registry.WRITE)
		if err != nil {
			return fmt.Errorf("create WER key %s: %w", target.RegistryPath, err)
		}
		if err := key.SetStringValue("DumpFolder", dumpDir); err != nil {
			_ = key.Close()
			return fmt.Errorf("set DumpFolder for %s: %w", target.Exe, err)
		}
		if err := key.SetDWordValue("DumpType", 2); err != nil {
			_ = key.Close()
			return fmt.Errorf("set DumpType for %s: %w", target.Exe, err)
		}
		if err := key.SetDWordValue("DumpCount", 5); err != nil {
			_ = key.Close()
			return fmt.Errorf("set DumpCount for %s: %w", target.Exe, err)
		}
		_ = key.Close()
	}
	return nil
}

// Package crashdump registers Windows Error Reporting local dumps for the
// desktop shell and its child processes.
package crashdump

import "path/filepath"

// Target describes one executable's WER local dump registration.
type Target struct {
	Exe          string
	RegistryPath string
	DumpFolder   string
}

// DumpDir is the per-user crash directory.
func DumpDir(dataDir string) string {
	return filepath.Join(dataDir, "crashes")
}

// RegistryPath returns the HKCU LocalDumps key for an executable.
func RegistryPath(exe string) string {
	return `Software\Microsoft\Windows\Windows Error Reporting\LocalDumps\` + exe
}

// Targets lists the shell, backend and sandbox executables.
func Targets(dataDir string) []Target {
	dumpDir := DumpDir(dataDir)
	names := []string{"ResearchAssistant.exe", "backend.exe", "sandbox.exe"}
	targets := make([]Target, 0, len(names))
	for _, name := range names {
		targets = append(targets, Target{Exe: name, RegistryPath: RegistryPath(name), DumpFolder: dumpDir})
	}
	return targets
}

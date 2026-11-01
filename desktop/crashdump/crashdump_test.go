package crashdump

import (
	"path/filepath"
	"testing"
)

func TestTargets(t *testing.T) {
	targets := Targets("/data")
	if len(targets) != 3 {
		t.Fatalf("len = %d", len(targets))
	}
	want := []string{"ResearchAssistant.exe", "backend.exe", "sandbox.exe"}
	for index, target := range targets {
		if target.Exe != want[index] {
			t.Fatalf("targets[%d] = %#v", index, target)
		}
		if target.DumpFolder != filepath.Join("/data", "crashes") {
			t.Fatalf("dump folder = %q", target.DumpFolder)
		}
	}
}

func TestRegistryPath(t *testing.T) {
	got := RegistryPath("backend.exe")
	want := "Software\\Microsoft\\Windows\\Windows Error Reporting\\LocalDumps\\backend.exe"
	if got != want {
		t.Fatalf("RegistryPath = %q, want %q", got, want)
	}
}

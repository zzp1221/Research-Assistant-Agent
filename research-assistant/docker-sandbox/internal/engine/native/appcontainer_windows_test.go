//go:build windows

package native

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

func testDACL(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

func TestAppContainerRuntimeRejectsJunctionWithoutSourceACLMutation(t *testing.T) {
	managed, outside, destination := t.TempDir(), t.TempDir(), t.TempDir()
	python := filepath.Join(managed, "python.exe")
	dll := filepath.Join(outside, "host.dll")
	for _, file := range []string{python, dll} {
		if err := os.WriteFile(file, []byte("test-owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	junction := filepath.Join(managed, "linked-lib")
	if out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create test-owned junction: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	rootBefore, targetBefore := testDACL(t, managed), testDACL(t, dll)
	if _, err := prepareAppContainerRuntimeCopy(python, destination, managed); err == nil {
		t.Fatal("runtime junction was copied")
	}
	if testDACL(t, managed) != rootBefore || testDACL(t, dll) != targetBefore {
		t.Fatal("rejected runtime changed a source or outside target ACL")
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed runtime copy left a partially published tree")
	}
}

func TestAppContainerRuntimeCopiesHardlinksIntoIndependentObjects(t *testing.T) {
	managed, outside := t.TempDir(), t.TempDir()
	python := filepath.Join(managed, "python.exe")
	dll := filepath.Join(outside, "host.dll")
	for _, file := range []string{python, dll} {
		if err := os.WriteFile(file, []byte("test-owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(dll, filepath.Join(managed, "linked.dll")); err != nil {
		t.Fatal(err)
	}
	rootBefore, targetBefore := testDACL(t, managed), testDACL(t, dll)
	copied, err := prepareAppContainerRuntimeCopy(python, t.TempDir(), managed)
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, _ := os.Stat(dll)
	copyInfo, err := os.Stat(filepath.Join(filepath.Dir(copied), "linked.dll"))
	if err != nil || os.SameFile(originalInfo, copyInfo) {
		t.Fatal("runtime copy retained a host hardlink")
	}
	if testDACL(t, managed) != rootBefore || testDACL(t, dll) != targetBefore {
		t.Fatal("runtime copy changed the source file object's ACL")
	}
	for _, online := range []bool{false, true} {
		security, err := appContainerCapabilities(online)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(testDACL(t, copied), security.AppContainerSid.String()) {
			t.Fatal("private copy is missing the runtime read/execute grant")
		}
		if strings.Contains(testDACL(t, dll), security.AppContainerSid.String()) {
			t.Fatal("source hardlink received a sandbox grant")
		}
	}
}

func TestAppContainerRuntimeValidatesBaseBeforeCopyingVenv(t *testing.T) {
	venv, unmanaged := t.TempDir(), t.TempDir()
	scripts := filepath.Join(venv, "Scripts")
	if err := os.Mkdir(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(scripts, "python.exe")
	if err := os.WriteFile(python, []byte("test-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = "+unmanaged+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := testDACL(t, venv)
	if _, err := prepareAppContainerRuntimeCopy(python, t.TempDir(), venv); err == nil {
		t.Fatal("unmanaged base runtime was copied")
	}
	if testDACL(t, venv) != before {
		t.Fatal("invalid base runtime changed the original venv ACL")
	}
}

func TestAppContainerRuntimeConcurrentSourceLinkReplacementCannotChangeHostDACL(t *testing.T) {
	managed, outside, destination := t.TempDir(), t.TempDir(), t.TempDir()
	python := filepath.Join(managed, "python.exe")
	victim := filepath.Join(managed, "module.dll")
	host := filepath.Join(outside, "host.dll")
	for _, path := range []string{python, victim, host} {
		if err := os.WriteFile(path, []byte("synthetic-runtime"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rootBefore, hostBefore := testDACL(t, outside), testDACL(t, host)
	var mutations atomic.Int32
	stop, stopped := make(chan struct{}), make(chan struct{})
	var stopOnce sync.Once
	stopWriter := func() { stopOnce.Do(func() { close(stop) }); <-stopped }
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(victim)
			if os.Link(host, victim) == nil {
				mutations.Add(1)
			}
			_ = os.Remove(victim)
			_ = os.WriteFile(victim, []byte("replacement-runtime"), 0o600)
		}
	}()
	defer stopWriter()
	// Racing the real filesystem may reject individual snapshots. Every
	// accepted snapshot must use independent file objects; rejection must
	// leave the source and outside DACLs unchanged.
	successfulCopies := 0
	for range 12 {
		copied, err := prepareAppContainerRuntimeCopy(python, destination, managed)
		if err == nil {
			successfulCopies++
			if err := filepath.WalkDir(filepath.Dir(copied), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				info, err := os.Stat(path)
				if err != nil {
					return err
				}
				hostInfo, _ := os.Stat(host)
				if os.SameFile(info, hostInfo) {
					t.Error("accepted runtime shares the outside file object")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if testDACL(t, outside) != rootBefore || testDACL(t, host) != hostBefore {
			t.Fatal("concurrent source replacement changed an outside DACL")
		}
	}
	stopWriter()
	if mutations.Load() == 0 {
		t.Fatal("concurrent hardlink replacement fixture never mutated the source")
	}
	t.Logf("%d real source hardlink replacements; %d independent accepted copies; outside DACLs unchanged", mutations.Load(), successfulCopies)
}

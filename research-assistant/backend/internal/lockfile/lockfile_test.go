package lockfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAcquireReleaseAndDeadProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	for range 2 {
		f, err := Acquire(path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if !strings.HasPrefix(string(data), strconv.Itoa(os.Getpid())+"\n") {
			t.Fatal(string(data))
		}
		if err := Release(f, path); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	Release(f, path)
}
func TestLiveAndMalformedLocksAreBounded(t *testing.T) {
	for _, data := range []string{fmt.Sprintf("%d\n", os.Getpid()), "bad pid", "", "-1"} {
		path := filepath.Join(t.TempDir(), "test.lock")
		os.WriteFile(path, []byte(data), 0600)
		start := time.Now()
		_, err := Acquire(path, 100*time.Millisecond)
		if !errors.Is(err, ErrTimeout) || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("data=%q elapsed=%s err=%v", data, time.Since(start), err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("deleted unconfirmed lock", err)
		}
	}
}

type failingLockWriter struct{ closed bool }

func (w *failingLockWriter) Write([]byte) (int, error) { return 0, io.ErrShortWrite }
func (w *failingLockWriter) Close() error              { w.closed = true; return nil }
func fakeOps() filesystemOps {
	return filesystemOps{
		create: func(string) (lockWriter, error) { return nil, os.ErrExist },
		read:   func(string) ([]byte, error) { return []byte("999999999\n"), nil },
		alive:  func(int) bool { return false }, remove: func(string) error { return nil },
	}
}
func TestRemoveFailureAndCompetition(t *testing.T) {
	for _, cause := range []error{os.ErrPermission, errors.New("read-only filesystem")} {
		ops := fakeOps()
		calls := 0
		ops.remove = func(string) error { calls++; return cause }
		start := time.Now()
		_, err := acquire("stale.lock", 40*time.Millisecond, ops)
		if !errors.Is(err, cause) || calls != 1 || time.Since(start) > 500*time.Millisecond || !strings.Contains(err.Error(), "remove stale lock") {
			t.Fatal(calls, err)
		}
	}
	ops := fakeOps()
	calls := 0
	ops.remove = func(string) error { calls++; return os.ErrNotExist }
	ops.create = func(string) (lockWriter, error) {
		if calls == 0 {
			return nil, os.ErrExist
		}
		return &bufferLock{}, nil
	}
	if _, err := acquire("race.lock", time.Second, ops); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	// Even the continuously disappearing stale path checks the deadline.
	ops = fakeOps()
	ops.remove = func(string) error { return os.ErrNotExist }
	start := time.Now()
	_, err := acquire("race.lock", time.Millisecond, ops)
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 500*time.Millisecond {
		t.Fatal(err)
	}
}

type bufferLock struct{ bytes.Buffer }

func (*bufferLock) Close() error { return nil }
func TestUnknownLivenessUnreadableAndWriteFailure(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		ops := fakeOps()
		calls := 0
		ops.alive = func(int) bool { return true }
		ops.remove = func(string) error { calls++; return nil }
		if unreadable {
			ops.read = func(string) ([]byte, error) { return nil, os.ErrPermission }
		}
		_, err := acquire("unknown.lock", time.Millisecond, ops)
		if !errors.Is(err, ErrTimeout) || calls != 0 {
			t.Fatal("removed unconfirmed lock", calls, err)
		}
	}
	writer := &failingLockWriter{}
	ops := fakeOps()
	removed := 0
	ops.create = func(string) (lockWriter, error) { return writer, nil }
	ops.remove = func(string) error { removed++; return nil }
	if _, err := acquire("write.lock", time.Second, ops); !errors.Is(err, io.ErrShortWrite) || !writer.closed || removed != 1 {
		t.Fatal("write failure cleanup", err)
	}
	ops.create = func(string) (lockWriter, error) { return nil, os.ErrPermission }
	if _, err := acquire("create.lock", time.Second, ops); !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "create lock") {
		t.Fatal(err)
	}
}
func TestNonpositiveTimeoutSingleAttempt(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		path := filepath.Join(t.TempDir(), "test.lock")
		f, err := Acquire(path, timeout)
		if err != nil {
			t.Fatal(err)
		}
		Release(f, path)
		ops := fakeOps()
		creates, reads, removes := 0, 0, 0
		ops.create = func(string) (lockWriter, error) { creates++; return nil, os.ErrExist }
		ops.read = func(string) ([]byte, error) { reads++; return []byte("99999"), nil }
		ops.remove = func(string) error { removes++; return nil }
		if _, err := acquire(path, timeout, ops); !errors.Is(err, ErrTimeout) || creates != 1 || reads != 0 || removes != 0 {
			t.Fatal(creates, reads, removes, err)
		}
	}
}

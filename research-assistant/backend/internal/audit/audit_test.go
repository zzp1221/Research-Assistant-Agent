package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndFilter(t *testing.T) {
	logger := &Logger{}
	logger.Record(Event{Action: "plan_created", Actor: "user-a", Target: "plan-1"})
	logger.Record(Event{Action: "plan_canceled", Actor: "user-b", Target: "plan-2"})
	all := logger.Events(10, "")
	if len(all) != 2 {
		t.Fatalf("events=%d", len(all))
	}
	mine := logger.Events(10, "user-a")
	if len(mine) != 1 || mine[0].Action != "plan_created" {
		t.Fatalf("filtered=%#v", mine)
	}
}

func TestConfigureWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	logger := &Logger{}
	if err := logger.Configure(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.file.Close() }()
	logger.Record(Event{Action: "upload_created", Actor: "u", Target: "upload-1"})
	raw, err := osReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || !contains(string(raw), "upload_created") {
		t.Fatalf("file=%q", string(raw))
	}
}

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

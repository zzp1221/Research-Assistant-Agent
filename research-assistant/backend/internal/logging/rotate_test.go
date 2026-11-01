package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatesWhenExceedingMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writer, err := Open(path, 64, 3)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer writer.Close()
	for i := 0; i < 10; i++ {
		if _, err := writer.Write([]byte(strings.Repeat("x", 20) + "\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", candidate, err)
		}
		if info.Size() > 64 {
			t.Fatalf("%s size = %d, want <= 64", candidate, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatalf("expected only 3 files including current, found .3")
	}
}

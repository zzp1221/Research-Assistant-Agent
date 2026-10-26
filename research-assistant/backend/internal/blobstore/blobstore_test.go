package blobstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPutDeduplicatesIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	data := bytes.Repeat([]byte("image-bytes"), 1024)
	ref1, size1, err := store.Put(data)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	ref2, size2, err := store.Put(data)
	if err != nil {
		t.Fatalf("put twice: %v", err)
	}
	if ref1 != ref2 {
		t.Fatalf("identical content must share one ref: %s vs %s", ref1, ref2)
	}
	if size1 != int64(len(data)) || size2 != size1 {
		t.Fatalf("unexpected sizes: %d %d", size1, size2)
	}
	loaded, err := store.Get(ref1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(loaded, data) {
		t.Fatalf("roundtrip mismatch")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "blobs", ref1[len("blob:"):][:2]))
	if len(entries) != 1 {
		t.Fatalf("expected exactly one stored blob file, got %d", len(entries))
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	store, _ := New(t.TempDir())
	for _, ref := range []string{"blob:../../etc/passwd", "blob:xyz", "../escape"} {
		if _, err := store.Path(ref); err == nil {
			t.Fatalf("expected invalid ref %q to be rejected", ref)
		}
	}
}

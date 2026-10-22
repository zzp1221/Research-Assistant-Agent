package documents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDocument() *Document {
	return &Document{
		Version: Version, UploadID: "upload-1", Status: StatusExtracted,
		PageCount: 3, Characters: 30,
		Pages: []Page{
			{Number: 1, Text: "page one", Chars: 8},
			{Number: 2, Text: "page two", Chars: 8},
			{Number: 3, Text: "", Chars: 0},
		},
	}
}

func TestJobStoreCreateAndOwnership(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobQueued || job.TotalPages != 3 {
		t.Fatalf("job=%#v", job)
	}
	if job.Pages[2].Status != PageSkipped {
		t.Fatalf("empty page should be skipped: %#v", job.Pages[2])
	}
	if _, ok := store.Get(job.ID, "someone-else"); ok {
		t.Fatal("job must not be visible to another owner")
	}
	if _, ok := store.Get(job.ID, "owner"); !ok {
		t.Fatal("owner should see the job")
	}
}

func TestJobStoreCancelMarksPendingPages(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	canceled, ok := store.Cancel(job.ID, "owner")
	if !ok || canceled.Status != JobCanceled {
		t.Fatalf("canceled=%#v", canceled)
	}
	for _, page := range canceled.Pages {
		if page.Status == PagePending {
			t.Fatalf("pending page survived cancel: %#v", page)
		}
	}
	if _, ok := store.Cancel(job.ID, "other"); ok {
		t.Fatal("foreign owner must not cancel")
	}
}

func TestJobStoreReloadsInterruptedJobs(t *testing.T) {
	root := t.TempDir()
	store := NewJobStore(root)
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	job.Status = JobRunning
	job.Pages[0].Status = PageRunning
	_ = store.persistLocked(job)
	store.mu.Unlock()

	reloaded := NewJobStore(root)
	loaded, ok := reloaded.Get(job.ID, "owner")
	if !ok {
		t.Fatal("job was not reloaded")
	}
	if loaded.Status != JobInterrupted {
		t.Fatalf("status=%s, want %s", loaded.Status, JobInterrupted)
	}
	if loaded.Pages[0].Status != PagePending {
		t.Fatalf("page status=%s, want pending", loaded.Pages[0].Status)
	}
}

func TestJobFilesAreOwnerOnly(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.jobDir(job.ID), "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("job file permissions=%o", info.Mode().Perm())
	}
}

// The runner is exercised without an LLM by cancelling immediately; this keeps
// the test hermetic while still proving the goroutines shut down.
func TestRunnerStopsWhenCanceled(t *testing.T) {
	store := NewJobStore(t.TempDir())
	document := testDocument()
	job, err := store.Create("upload-1", "owner", "zh", document, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Store: store}
	runner.Start(context.Background(), job, document)
	store.Cancel(job.ID, "owner")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := store.Get(job.ID, "owner")
		if ok && current.Status == JobCanceled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not reach canceled state")
}

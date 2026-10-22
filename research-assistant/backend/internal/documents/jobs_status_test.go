package documents

import (
	"testing"
)

// mutateCanonical runs fn against the store-owned job, mirroring what the
// runner does while pages finish.
func mutateCanonical(store *JobStore, job *TranslationJob, fn func(*TranslationJob)) {
	store.mu.Lock()
	defer store.mu.Unlock()
	canonical := store.jobs[job.ID]
	canonical.Status = JobRunning
	fn(canonical)
}

func TestFinishMarksPartialWhenSomePagesFail(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	mutateCanonical(store, job, func(canonical *TranslationJob) {
		canonical.Pages[0].Status = PageCompleted
		canonical.Pages[1].Status = PageFailed
		canonical.Pages[1].Error = "LLM returned HTTP 402"
	})
	runner := &Runner{Store: store}
	runner.finish(job, nil)
	current, ok := store.Get(job.ID, "owner")
	if !ok {
		t.Fatal("job disappeared")
	}
	if current.Status != JobPartial {
		t.Fatalf("status=%s, want %s", current.Status, JobPartial)
	}
	if current.CompletedPages != 1 || current.FailedPages != 1 {
		t.Fatalf("progress=%d/%d", current.CompletedPages, current.FailedPages)
	}
	if current.Error == "" {
		t.Fatal("partial job should explain the failure")
	}
}

func TestFinishMarksFailedWhenAllPagesFail(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	mutateCanonical(store, job, func(canonical *TranslationJob) {
		canonical.Pages[0].Status = PageFailed
		canonical.Pages[1].Status = PageFailed
	})
	runner := &Runner{Store: store}
	runner.finish(job, nil)
	current, _ := store.Get(job.ID, "owner")
	if current.Status != JobFailed {
		t.Fatalf("status=%s, want %s", current.Status, JobFailed)
	}
}

func TestFinishMarksCompletedWhenAllPagesSucceed(t *testing.T) {
	store := NewJobStore(t.TempDir())
	job, err := store.Create("upload-1", "owner", "zh", testDocument(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	mutateCanonical(store, job, func(canonical *TranslationJob) {
		canonical.Pages[0].Status = PageCompleted
		canonical.Pages[1].Status = PageCompleted
	})
	runner := &Runner{Store: store}
	runner.finish(job, nil)
	current, _ := store.Get(job.ID, "owner")
	if current.Status != JobCompleted {
		t.Fatalf("status=%s, want %s", current.Status, JobCompleted)
	}
}

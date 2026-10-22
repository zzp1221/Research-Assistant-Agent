package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"research-assistant-backend/internal/assist"
)

// Translation job states.
const (
	JobQueued      = "queued"
	JobRunning     = "running"
	JobCompleted   = "completed"
	JobPartial     = "partial"
	JobFailed      = "failed"
	JobCanceled    = "canceled"
	JobInterrupted = "interrupted"
)

const (
	PagePending   = "pending"
	PageRunning   = "running"
	PageCompleted = "completed"
	PageFailed    = "failed"
	PageSkipped   = "skipped"
	PageCanceled  = "canceled"
)

// PageJob tracks one page inside a translation job.
type PageJob struct {
	Number    int       `json:"number"`
	Status    string    `json:"status"`
	Chars     int       `json:"chars"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TranslationJob is the durable progress record for one full-document translation.
type TranslationJob struct {
	ID             string     `json:"id"`
	UploadID       string     `json:"upload_id"`
	OwnerID        string     `json:"owner_id"`
	TargetLang     string     `json:"target_lang"`
	Status         string     `json:"status"`
	TotalPages     int        `json:"total_pages"`
	CompletedPages int        `json:"completed_pages"`
	FailedPages    int        `json:"failed_pages"`
	CurrentPage    int        `json:"current_page"`
	Pages          []PageJob  `json:"pages"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// JobStore persists translation jobs next to their uploaded document.
type JobStore struct {
	mu     sync.Mutex
	root   string
	jobs   map[string]*TranslationJob
	cancel map[string]context.CancelFunc
}

// NewJobStore loads any jobs previously written under root.
func NewJobStore(root string) *JobStore {
	store := &JobStore{root: root, jobs: map[string]*TranslationJob{}, cancel: map[string]context.CancelFunc{}}
	store.load()
	return store
}

func (s *JobStore) load() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, entry.Name(), "job.json"))
		if err != nil {
			continue
		}
		var job TranslationJob
		if err := json.Unmarshal(raw, &job); err != nil || job.ID == "" {
			continue
		}
		if job.Status == JobRunning || job.Status == JobQueued {
			job.Status = JobInterrupted
			for index := range job.Pages {
				if job.Pages[index].Status == PageRunning {
					job.Pages[index].Status = PagePending
				}
			}
		}
		s.jobs[job.ID] = &job
	}
}

func (s *JobStore) jobDir(jobID string) string { return filepath.Join(s.root, jobID) }

func (s *JobStore) persistLocked(job *TranslationJob) error {
	if err := os.MkdirAll(s.jobDir(job.ID), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.jobDir(job.ID), "job.json"), raw, 0o600)
}

// Create registers a new job for every requested page and persists it.
func (s *JobStore) Create(uploadID, ownerID, targetLang string, document *Document, startPage, endPage int) (*TranslationJob, error) {
	if document == nil || !document.HasText() {
		return nil, fmt.Errorf("document has no extractable text")
	}
	if startPage < 1 {
		startPage = 1
	}
	if endPage < 1 || endPage > document.PageCount {
		endPage = document.PageCount
	}
	if startPage > endPage {
		return nil, fmt.Errorf("start_page must not exceed end_page")
	}
	job := &TranslationJob{
		ID: newJobID(), UploadID: uploadID, OwnerID: ownerID,
		TargetLang: targetLang, Status: JobQueued,
		TotalPages: endPage - startPage + 1, CurrentPage: startPage,
		Pages:     make([]PageJob, 0, endPage-startPage+1),
		CreatedAt: time.Now().UTC(),
	}
	for page := startPage; page <= endPage; page++ {
		chars := 0
		text := ""
		if index := page - 1; index >= 0 && index < len(document.Pages) {
			chars = document.Pages[index].Chars
			text = document.Pages[index].Text
		}
		status := PagePending
		if text == "" {
			status = PageSkipped
		}
		job.Pages = append(job.Pages, PageJob{Number: page, Status: status, Chars: chars, UpdatedAt: time.Now().UTC()})
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	err := s.persistLocked(job)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return cloneJob(job), nil
}

// Get returns a copy owned by ownerID.
func (s *JobStore) Get(jobID, ownerID string) (*TranslationJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok || job.OwnerID != ownerID {
		return nil, false
	}
	return cloneJob(job), true
}

// List returns jobs for one upload owned by ownerID, newest first.
func (s *JobStore) List(uploadID, ownerID string) []*TranslationJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TranslationJob, 0)
	for _, job := range s.jobs {
		if job.UploadID == uploadID && job.OwnerID == ownerID {
			out = append(out, cloneJob(job))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Cancel marks a queued/running job canceled and stops its workers.
func (s *JobStore) Cancel(jobID, ownerID string) (*TranslationJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok || job.OwnerID != ownerID {
		return nil, false
	}
	if job.Status == JobCompleted || job.Status == JobCanceled || job.Status == JobFailed {
		return cloneJob(job), true
	}
	job.Status = JobCanceled
	now := time.Now().UTC()
	job.FinishedAt = &now
	job.CurrentPage = 0
	for index := range job.Pages {
		if job.Pages[index].Status == PagePending || job.Pages[index].Status == PageRunning {
			job.Pages[index].Status = PageCanceled
			job.Pages[index].UpdatedAt = now
		}
	}
	if cancel, exists := s.cancel[jobID]; exists {
		cancel()
		delete(s.cancel, jobID)
	}
	_ = s.persistLocked(job)
	return cloneJob(job), true
}

// PageText returns the stored translation for one page.
func (s *JobStore) PageText(jobID, ownerID string, page int) (string, *TranslationJob, bool) {
	job, ok := s.Get(jobID, ownerID)
	if !ok {
		return "", nil, false
	}
	raw, err := os.ReadFile(filepath.Join(s.jobDir(jobID), fmt.Sprintf("page-%04d.txt", page)))
	if err != nil {
		return "", job, false
	}
	return string(raw), job, true
}

// Runner translates pages with bounded concurrency and persists every result.
type Runner struct {
	Store    *JobStore
	CacheDir string
}

// Start launches a job. It returns immediately; progress is visible via Store.
// The caller must pass a detached context (for example context.Background):
// an HTTP request context would be cancelled as soon as the response is sent.
func (r *Runner) Start(parent context.Context, job *TranslationJob, document *Document) {
	if job == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.Store.mu.Lock()
	canonical, ok := r.Store.jobs[job.ID]
	if !ok {
		r.Store.mu.Unlock()
		cancel()
		return
	}
	job = canonical
	job.Status = JobRunning
	now := time.Now().UTC()
	job.StartedAt = &now
	r.Store.cancel[job.ID] = cancel
	_ = r.Store.persistLocked(job)
	// Snapshot the work under the store lock: Cancel mutates job.Pages from
	// another goroutine, so the runner must not iterate the shared slice later.
	pendingPages := make([]int, 0, len(job.Pages))
	for index := range job.Pages {
		if job.Pages[index].Status == PagePending {
			pendingPages = append(pendingPages, job.Pages[index].Number)
		}
	}
	totalPages := len(job.Pages)
	r.Store.mu.Unlock()

	go func() {
		defer cancel()
		pending := make(chan int, len(pendingPages))
		for _, page := range pendingPages {
			pending <- page
		}
		close(pending)

		workers := 3
		if workers > totalPages {
			workers = totalPages
		}
		if workers < 1 {
			workers = 1
		}
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for page := range pending {
					if ctx.Err() != nil {
						return
					}
					r.translatePage(ctx, job, document, page)
				}
			}()
		}
		wg.Wait()
		r.finish(job, ctx.Err())
	}()
}

func (r *Runner) translatePage(ctx context.Context, job *TranslationJob, document *Document, page int) {
	r.Store.mu.Lock()
	if job.Status == JobCanceled {
		r.Store.mu.Unlock()
		return
	}
	job.CurrentPage = page
	for index := range job.Pages {
		if job.Pages[index].Number == page {
			job.Pages[index].Status = PageRunning
			job.Pages[index].UpdatedAt = time.Now().UTC()
		}
	}
	_ = r.Store.persistLocked(job)
	r.Store.mu.Unlock()

	text := ""
	if index := page - 1; index >= 0 && index < len(document.Pages) {
		text = document.Pages[index].Text
	}
	var (
		result *assist.Result
		err    error
	)
	if text == "" {
		err = fmt.Errorf("page %d has no text", page)
	} else {
		result, err = assist.TranslateOrExplain(ctx, r.CacheDir, documentSHA(document), assist.Request{
			Mode: "translate", Text: text, Page: page, Context: ContextForPage(document, page, 1),
		})
	}

	r.Store.mu.Lock()
	defer r.Store.mu.Unlock()
	if job.Status == JobCanceled {
		return
	}
	for index := range job.Pages {
		if job.Pages[index].Number != page {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				job.Pages[index].Status = PageCanceled
			} else {
				job.Pages[index].Status = PageFailed
				job.Pages[index].Error = err.Error()
			}
		} else {
			job.Pages[index].Status = PageCompleted
			_ = os.MkdirAll(r.Store.jobDir(job.ID), 0o700)
			_ = os.WriteFile(filepath.Join(r.Store.jobDir(job.ID), fmt.Sprintf("page-%04d.txt", page)), []byte(result.Result), 0o600)
		}
		job.Pages[index].UpdatedAt = time.Now().UTC()
	}
	r.recountLocked(job)
	_ = r.Store.persistLocked(job)
}

func (r *Runner) finish(job *TranslationJob, ctxErr error) {
	r.Store.mu.Lock()
	defer r.Store.mu.Unlock()
	canonical, ok := r.Store.jobs[job.ID]
	if !ok {
		return
	}
	job = canonical
	if job.Status == JobCanceled {
		return
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	job.CurrentPage = 0
	r.recountLocked(job)
	switch {
	case ctxErr != nil:
		job.Status = JobInterrupted
	case job.FailedPages == job.TotalPages:
		job.Status = JobFailed
		job.Error = "all pages failed"
	case job.FailedPages > 0:
		// A job that only translated part of the document must never look like a
		// complete translation; the UI shows the failed pages for retry.
		job.Status = JobPartial
		job.Error = fmt.Sprintf("%d of %d pages failed", job.FailedPages, job.TotalPages)
	default:
		job.Status = JobCompleted
	}
	_ = r.Store.persistLocked(job)
	delete(r.Store.cancel, job.ID)
}

func (r *Runner) recountLocked(job *TranslationJob) {
	completed, failed := 0, 0
	for _, page := range job.Pages {
		switch page.Status {
		case PageCompleted, PageSkipped:
			completed++
		case PageFailed:
			failed++
		}
	}
	job.CompletedPages = completed
	job.FailedPages = failed
}

func documentSHA(document *Document) string {
	if document == nil {
		return ""
	}
	return document.UploadID + ":" + fmt.Sprint(document.Characters)
}

func cloneJob(job *TranslationJob) *TranslationJob {
	if job == nil {
		return nil
	}
	copyJob := *job
	copyJob.Pages = append([]PageJob(nil), job.Pages...)
	return &copyJob
}

func newJobID() string {
	return fmt.Sprintf("job-%d", time.Now().UnixNano())
}

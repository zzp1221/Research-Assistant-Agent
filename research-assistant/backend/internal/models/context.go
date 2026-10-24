package models

// ResearchContext is the typed, plan-level context shared by every planner
// branch and agent. It is persisted with the plan so the scheduler can rebuild
// task inputs after a restart.
type ResearchContext struct {
	Version     string          `json:"version"`
	UserIntent  string          `json:"user_intent"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	Documents   []DocumentRef   `json:"documents,omitempty"`
	Paper       *PaperMetadata  `json:"paper,omitempty"`
	Entities    map[string]any  `json:"entities,omitempty"`
	Constraints map[string]any  `json:"constraints,omitempty"`
}

type AttachmentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	DocumentID  string `json:"document_id,omitempty"`
	PageCount   int    `json:"page_count,omitempty"`
	Role        string `json:"role,omitempty"` // paper / dataset / config / supplementary / unknown
}

type DocumentRef struct {
	DocumentID string `json:"document_id"`
	UploadID   string `json:"upload_id"`
	PageCount  int    `json:"page_count,omitempty"`
	CharCount  int    `json:"char_count,omitempty"`
}

type PaperMetadata struct {
	Title    string   `json:"title,omitempty"`
	Authors  []string `json:"authors,omitempty"`
	Year     int      `json:"year,omitempty"`
	Venue    string   `json:"venue,omitempty"`
	ArxivID  string   `json:"arxiv_id,omitempty"`
	RepoURL  string   `json:"repo_url,omitempty"`
	Abstract string   `json:"abstract,omitempty"`
}

type MissingRequirement struct {
	Kind        string `json:"kind"` // attachment / document / paper_metadata / image / dependency / gpu / dataset / model / disk / run_command
	Name        string `json:"name,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

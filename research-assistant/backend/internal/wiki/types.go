// Package wiki implements the Paper Wiki read model described in
// docs/design/2026-11-02-paper-wiki-memory.md. Point and Link are memory.Record
// views; PaperPage is a rebuildable projection cache. Nothing in this package is
// a second source of truth.
package wiki

import (
	"time"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
)

// PointKind is the atomic memory kind shown on a paper page.
type PointKind string

const (
	PointClaim        PointKind = "claim"
	PointMethod       PointKind = "method"
	PointResult       PointKind = "result"
	PointLimitation   PointKind = "limitation"
	PointTask         PointKind = "task"
	PointProcedure    PointKind = "procedure"
	PointNegative     PointKind = "negative"
	PointOpenQuestion PointKind = "open_question"
	PointUserNote     PointKind = "user_note"
	PointConstraint   PointKind = "constraint"
)

// Valid reports whether the kind is part of the documented taxonomy.
func (k PointKind) Valid() bool {
	switch k {
	case PointClaim, PointMethod, PointResult, PointLimitation, PointTask,
		PointProcedure, PointNegative, PointOpenQuestion, PointUserNote, PointConstraint:
		return true
	}
	return false
}

// MemoryKind maps a wiki point kind onto the underlying memory record kind.
func (k PointKind) MemoryKind() memory.Kind {
	switch k {
	case PointTask:
		return memory.KindEpisode
	case PointProcedure:
		return memory.KindProcedure
	case PointNegative:
		return memory.KindNegative
	case PointConstraint:
		return memory.KindConstraint
	default:
		return memory.KindFact
	}
}

// Point is a read-only view over one memory.Record tagged wiki_point.
type Point struct {
	Record  memory.Record      `json:"record"`
	PaperID string             `json:"paper_id"`
	Kind    PointKind          `json:"kind"`
	Refs    []memory.SourceRef `json:"refs,omitempty"`
}

// LinkType is the typed cross-paper relation.
type LinkType string

const (
	LinkCites       LinkType = "cites"
	LinkExtends     LinkType = "extends"
	LinkContradicts LinkType = "contradicts"
	LinkSameDataset LinkType = "uses_same_dataset"
	LinkSameMethod  LinkType = "uses_same_method"
	LinkSameRepo    LinkType = "same_repo"
	LinkReproduces  LinkType = "reproduces"
	LinkRelated     LinkType = "related"
	LinkSupersedes  LinkType = "supersedes"
)

// Valid reports whether the link type is supported.
func (t LinkType) Valid() bool {
	switch t {
	case LinkCites, LinkExtends, LinkContradicts, LinkSameDataset, LinkSameMethod,
		LinkSameRepo, LinkReproduces, LinkRelated, LinkSupersedes:
		return true
	}
	return false
}

// Symmetric reports whether the relation must be normalized to a canonical
// direction (from_paper < to_paper) to avoid duplicate A->B / B->A rows.
func (t LinkType) Symmetric() bool {
	switch t {
	case LinkContradicts, LinkSameDataset, LinkSameMethod, LinkSameRepo, LinkRelated:
		return true
	}
	return false
}

// Link maps from a memory.Record tagged wiki_link.
type Link struct {
	ID           string             `json:"id"`
	OwnerID      string             `json:"owner_id"`
	ProjectID    string             `json:"project_id,omitempty"`
	FromPaper    string             `json:"from_paper"`
	ToPaper      string             `json:"to_paper"`
	Type         LinkType           `json:"type"`
	Evidence     []memory.SourceRef `json:"evidence,omitempty"`
	Source       string             `json:"source"`
	Confidence   float64            `json:"confidence"`
	Status       memory.Status      `json:"status"`
	Revision     int                `json:"revision"`
	SupersededBy string             `json:"superseded_by,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

// ReproductionStatus summarizes the project's reproduction attempts for a paper.
type ReproductionStatus struct {
	Status     string `json:"status"` // none / running / succeeded / failed / blocked / cancelled
	LastPlanID string `json:"last_plan_id,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// PaperPage is the user-readable projection for one paper. It stores only
// metadata and ids; sections are documents.SectionInfo and points/links live in
// the memory store.
type PaperPage struct {
	PaperID      string                  `json:"paper_id"`
	CanonicalKey string                  `json:"canonical_key,omitempty"`
	OwnerID      string                  `json:"owner_id"`
	ProjectID    string                  `json:"project_id,omitempty"`
	Title        string                  `json:"title"`
	Authors      []string                `json:"authors,omitempty"`
	Year         int                     `json:"year,omitempty"`
	Venue        string                  `json:"venue,omitempty"`
	ArxivID      string                  `json:"arxiv_id,omitempty"`
	DOI          string                  `json:"doi,omitempty"`
	Abstract     string                  `json:"abstract,omitempty"`
	TLDR         string                  `json:"tldr,omitempty"`
	TLDRInferred bool                    `json:"tldr_inferred,omitempty"`
	Sections     []documents.SectionInfo `json:"sections,omitempty"`
	PointIDs     []string                `json:"point_ids,omitempty"`
	LinkIDs      []string                `json:"link_ids,omitempty"`
	Reproduction ReproductionStatus      `json:"reproduction"`
	DocumentIDs  []string                `json:"document_ids,omitempty"`
	Entities     []memory.Entity         `json:"entities,omitempty"`
	Archived     bool                    `json:"archived,omitempty"`
	Revision     int                     `json:"revision"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at"`
}

// PaperQuery filters the paper list.
type PaperQuery struct {
	OwnerID   string
	ProjectID string
	Text      string
	Status    string // active | archived | all
	Limit     int
	Cursor    int
}

// Stats summarizes one owner's wiki.
type Stats struct {
	Papers       int            `json:"papers"`
	Points       int            `json:"points"`
	Links        int            `json:"links"`
	PendingLinks int            `json:"pending_links"`
	ByKind       map[string]int `json:"by_kind,omitempty"`
}

// TaskQuery asks the wiki for the context of one task.
type TaskQuery struct {
	OwnerID     string
	ProjectID   string
	SessionID   string
	Text        string
	Entities    []memory.Entity
	Limit       int
	PromptLimit int
}

// PaperContext is one paper plus its most relevant points and links.
type PaperContext struct {
	Paper  PaperPage `json:"paper"`
	Points []Point   `json:"points,omitempty"`
	Links  []Link    `json:"links,omitempty"`
}

// WikiContext is the bounded block injected into a prompt.
type WikiContext struct {
	Text      string            `json:"text,omitempty"`
	Papers    []PaperContext    `json:"papers,omitempty"`
	Citations []memory.Citation `json:"citations,omitempty"`
	PaperIDs  []string          `json:"paper_ids,omitempty"`
	Tokens    int               `json:"tokens,omitempty"`
}

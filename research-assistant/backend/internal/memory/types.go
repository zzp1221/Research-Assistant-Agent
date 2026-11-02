package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Kind is the domain category of a memory record.
type Kind string

const (
	KindProfile    Kind = "profile"
	KindFact       Kind = "fact"
	KindEpisode    Kind = "episode"
	KindProcedure  Kind = "procedure"
	KindConstraint Kind = "constraint"
	KindNegative   Kind = "negative"
	KindDocument   Kind = "document"
	KindWorking    Kind = "working"
	KindReflection Kind = "reflection"
)

func (k Kind) Valid() bool {
	switch k {
	case KindProfile, KindFact, KindEpisode, KindProcedure, KindConstraint, KindNegative, KindDocument, KindWorking, KindReflection:
		return true
	}
	return false
}

// Trust describes how much confidence the system places in a memory.
type Trust string

const (
	TrustUserExplicit Trust = "user_explicit"
	TrustVerified     Trust = "verified"
	TrustInferred     Trust = "inferred"
	TrustUntrusted    Trust = "untrusted"
)

func (t Trust) Valid() bool {
	switch t {
	case TrustUserExplicit, TrustVerified, TrustInferred, TrustUntrusted:
		return true
	}
	return false
}

// Status is the lifecycle state of a record. Old facts are never physically
// deleted: they become superseded so the history remains answerable.
type Status string

const (
	StatusActive     Status = "active"
	StatusPending    Status = "pending_review"
	StatusSuperseded Status = "superseded"
	StatusArchived   Status = "archived"
	StatusDeleted    Status = "deleted"
)

func (s Status) Valid() bool {
	switch s {
	case StatusActive, StatusPending, StatusSuperseded, StatusArchived, StatusDeleted:
		return true
	}
	return false
}

// Scope determines who can retrieve a record. Empty fields mean "applies to
// the whole parent scope".
type Scope struct {
	OwnerID   string `json:"owner_id"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// Source points at the evidence that produced a record.
type Source struct {
	Type      string    `json:"type"`
	Ref       string    `json:"ref,omitempty"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// Evidence captures provenance and sensitivity of a record.
type Evidence struct {
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence,omitempty"`
	SHA256     string  `json:"sha256,omitempty"`
	Hidden     bool    `json:"hidden,omitempty"`
}

// SourceRef is one external piece of evidence a memory points at. It is
// deliberately separate from Evidence: Evidence says how trustworthy the
// record is, SourceRef says which page/section/quote/artifact it cites.
type SourceRef struct {
	DocumentID  string `json:"document_id,omitempty"`
	BlockID     string `json:"block_id,omitempty"`
	Page        int    `json:"page,omitempty"`
	Section     string `json:"section,omitempty"`
	Quote       string `json:"quote,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
}

// Validity is the bi-temporal window of a fact.
type Validity struct {
	ValidFrom    *time.Time `json:"valid_from,omitempty"`
	ValidTo      *time.Time `json:"valid_to,omitempty"`
	RecordedAt   time.Time  `json:"recorded_at,omitempty"`
	Supersedes   []string   `json:"supersedes,omitempty"`
	SupersededBy string     `json:"superseded_by,omitempty"`
}

// Entity is a normalized reference used for entity recall and linking.
type Entity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Record is one durable memory.
type Record struct {
	ID           string         `json:"id"`
	Version      int            `json:"version"`
	Kind         Kind           `json:"kind"`
	Scope        Scope          `json:"scope"`
	Title        string         `json:"title"`
	Content      string         `json:"content"`
	Tags         []string       `json:"tags,omitempty"`
	Entities     []Entity       `json:"entities,omitempty"`
	Source       Source         `json:"source"`
	Evidence     Evidence       `json:"evidence"`
	Refs         []SourceRef    `json:"refs,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	Validity     Validity       `json:"validity"`
	Importance   int            `json:"importance"`
	Confidence   float64        `json:"confidence,omitempty"`
	Trust        Trust          `json:"trust"`
	Sensitivity  string         `json:"sensitivity,omitempty"`
	Status       Status         `json:"status"`
	Pinned       bool           `json:"pinned,omitempty"`
	UseCount     int            `json:"use_count,omitempty"`
	LastUsedAt   *time.Time     `json:"last_used_at,omitempty"`
	ContentHash  string         `json:"content_hash,omitempty"`
	Tokens       int            `json:"tokens,omitempty"`
	DerivedFrom  []string       `json:"derived_from,omitempty"`
	PromotedFrom string         `json:"promoted_from,omitempty"`
	Conflict     bool           `json:"conflict,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Normalize fills default fields and computes the content hash. It is called
// on every write path so callers cannot persist an inconsistent record.
func (r *Record) Normalize(now time.Time) {
	if r.Kind == "" {
		r.Kind = KindFact
	}
	if r.Trust == "" {
		r.Trust = TrustVerified
	}
	if r.Status == "" {
		r.Status = StatusActive
	}
	if r.Sensitivity == "" {
		r.Sensitivity = "private"
	}
	if r.Importance <= 0 {
		r.Importance = 5
	}
	if r.Importance > 10 {
		r.Importance = 10
	}
	if r.Confidence <= 0 {
		r.Confidence = 0.8
	}
	if r.Version <= 0 {
		r.Version = 1
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if r.Validity.RecordedAt.IsZero() {
		r.Validity.RecordedAt = now
	}
	r.ContentHash = ContentHash(r.Kind, r.Scope, r.Title, r.Content)
	r.Tokens = EstimateTokens(RenderPlain(*r))
}

// ContentHash returns sha256(kind + scope + normalized(title + content)).
func ContentHash(kind Kind, scope Scope, title, content string) string {
	payload := string(kind) + "|" + scope.OwnerID + "|" + scope.ProjectID + "|" + scope.SessionID + "|" + scope.PlanID + "|" + scope.AgentID + "|" + NormalizeText(title) + "|" + NormalizeText(content)
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FactKey returns a stable key for a subject/attribute fact.
func FactKey(scope Scope, kind Kind, subject, attribute string) string {
	payload := scope.OwnerID + "|" + scope.ProjectID + "|" + string(kind) + "|" + NormalizeText(subject) + "|" + NormalizeText(attribute)
	sum := sha256.Sum256([]byte(payload))
	return "factkey:" + hex.EncodeToString(sum[:])
}

// ProcedureKey returns a stable key for a trigger-scoped procedure.
func ProcedureKey(scope Scope, agent, environment, trigger string) string {
	payload := scope.OwnerID + "|" + scope.ProjectID + "|" + NormalizeText(agent) + "|" + NormalizeText(environment) + "|" + NormalizeText(trigger)
	sum := sha256.Sum256([]byte(payload))
	return "prockey:" + hex.EncodeToString(sum[:])
}

// NormalizeText lowercases and collapses whitespace for hashing and matching.
func NormalizeText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

// Query describes one retrieval request.
type Query struct {
	OwnerID   string
	ProjectID string
	SessionID string
	PlanID    string
	AgentID   string
	Text      string
	Kinds     []Kind
	Statuses  []Status
	Tags      []string
	Entities  []Entity
	Limit     int
	TopK      int
	// IncludeSuperseded is used by as-of/history queries. Injection never sets it.
	IncludeSuperseded bool
	IncludePending    bool
	AsOf              *time.Time
}

// ScoredRecord is a retrieval candidate with its ranking diagnostics.
type ScoredRecord struct {
	Record      Record  `json:"record"`
	Score       float64 `json:"score"`
	BM25        float64 `json:"bm25,omitempty"`
	EntityHit   bool    `json:"entity_hit,omitempty"`
	RecencyHit  bool    `json:"recency_hit,omitempty"`
	PinnedBonus bool    `json:"pinned_bonus,omitempty"`
}

// Citation is the user-visible reference for an injected memory. PaperID,
// PointID, Page, Section and Quote are optional wiki extensions; older clients
// that only read the original fields keep working.
type Citation struct {
	MemoryID string `json:"memory_id"`
	Title    string `json:"title"`
	Kind     Kind   `json:"kind"`
	Date     string `json:"date,omitempty"`
	Source   string `json:"source,omitempty"`
	Trust    Trust  `json:"trust,omitempty"`
	PaperID  string `json:"paper_id,omitempty"`
	PointID  string `json:"point_id,omitempty"`
	Page     int    `json:"page,omitempty"`
	Section  string `json:"section,omitempty"`
	Quote    string `json:"quote,omitempty"`
}

// Context is the rendered memory context for one prompt.
type Context struct {
	Core          []Record       `json:"core,omitempty"`
	Retrieved     []Record       `json:"retrieved,omitempty"`
	Citations     []Citation     `json:"citations,omitempty"`
	CoreText      string         `json:"core_text,omitempty"`
	RetrievedText string         `json:"retrieved_text,omitempty"`
	Tokens        int            `json:"tokens,omitempty"`
	Report        RetrieveReport `json:"report,omitempty"`
}

// RetrieveReport records how the budget was spent.
type RetrieveReport struct {
	Candidates      int `json:"candidates"`
	Selected        int `json:"selected"`
	CoreTokens      int `json:"core_tokens"`
	RetrievedTokens int `json:"retrieved_tokens"`
	Truncated       int `json:"truncated,omitempty"`
}

// Stats summarizes one owner's memory store.
type Stats struct {
	Total     int            `json:"total"`
	Active    int            `json:"active"`
	ByKind    map[string]int `json:"by_kind"`
	ByStatus  map[string]int `json:"by_status"`
	Tokens    int            `json:"tokens"`
	Pinned    int            `json:"pinned"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
}

// Settings is the per-owner memory configuration.
type Settings struct {
	Enabled               bool   `json:"enabled"`
	Mode                  string `json:"mode"` // on | shadow | off
	RetrievalTopK         int    `json:"retrieval_topk"`
	CoreBudgetTokens      int    `json:"core_budget_tokens"`
	RetrievedBudgetTokens int    `json:"retrieved_budget_tokens"`
	LLMExtraction         bool   `json:"llm_extraction"`
	RetentionDays         int    `json:"retention_days"`
}

// DefaultSettings returns the documented defaults.
func DefaultSettings() Settings {
	return Settings{
		Enabled:               true,
		Mode:                  "on",
		RetrievalTopK:         8,
		CoreBudgetTokens:      512,
		RetrievedBudgetTokens: 2000,
		LLMExtraction:         false,
		RetentionDays:         180,
	}
}

// Patch is a partial update for PATCH /api/memories/:id.
type Patch struct {
	Title      *string         `json:"title,omitempty"`
	Content    *string         `json:"content,omitempty"`
	Tags       *[]string       `json:"tags,omitempty"`
	Importance *int            `json:"importance,omitempty"`
	Pinned     *bool           `json:"pinned,omitempty"`
	Status     *Status         `json:"status,omitempty"`
	Refs       *[]SourceRef    `json:"refs,omitempty"`
	Attributes *map[string]any `json:"attributes,omitempty"`
}

package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

const (
	// TagWikiPoint marks a memory record that is a wiki point.
	TagWikiPoint = "wiki_point"
	// TagWikiLink marks a memory record that is a typed cross-paper link.
	TagWikiLink    = "wiki_link"
	paperTagPrefix = "paper:"
	pointTagPrefix = "point:"
	linkTagPrefix  = "link:"
)

// PaperTag returns the paper tag for one canonical paper id.
func PaperTag(paperID string) string { return paperTagPrefix + strings.TrimSpace(paperID) }

// PointTag returns the point-kind tag.
func PointTag(kind PointKind) string { return pointTagPrefix + string(kind) }

// LinkTag returns the link-type tag.
func LinkTag(linkType LinkType) string { return linkTagPrefix + string(linkType) }

// PaperIDFromRecord extracts the paper id from a wiki point record.
func PaperIDFromRecord(record memory.Record) string {
	for _, tag := range record.Tags {
		if strings.HasPrefix(tag, paperTagPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(tag, paperTagPrefix))
		}
	}
	return ""
}

// PointKindFromRecord extracts the point kind from a wiki point record.
func PointKindFromRecord(record memory.Record) PointKind {
	for _, tag := range record.Tags {
		if strings.HasPrefix(tag, pointTagPrefix) {
			return PointKind(strings.TrimSpace(strings.TrimPrefix(tag, pointTagPrefix)))
		}
	}
	return ""
}

// ParsePoint converts a memory record into its wiki view when tagged.
func ParsePoint(record memory.Record) (Point, bool) {
	if !hasTag(record.Tags, TagWikiPoint) {
		return Point{}, false
	}
	paperID := PaperIDFromRecord(record)
	kind := PointKindFromRecord(record)
	if paperID == "" || !kind.Valid() {
		return Point{}, false
	}
	return Point{Record: record, PaperID: paperID, Kind: kind, Refs: record.Refs}, true
}

// PointInput describes one point to persist.
type PointInput struct {
	OwnerID       string
	ProjectID     string
	PaperID       string
	Kind          PointKind
	Title         string
	Text          string
	Refs          []memory.SourceRef
	Trust         memory.Trust
	Status        memory.Status
	SourceType    string
	SourceRef     string
	Importance    int
	Confidence    float64
	EvidenceClass string
	Attributes    map[string]any
	PlanID        string
	TaskID        string
}

// CreatePoint validates and persists one wiki point. The record is a normal
// memory.Record; there is no separate point store.
func (b *Builder) CreatePoint(ctx context.Context, input PointInput) (Point, error) {
	if b == nil || b.Memory == nil || b.Store == nil {
		return Point{}, fmt.Errorf("wiki point store is not configured")
	}
	input.OwnerID = strings.TrimSpace(input.OwnerID)
	input.PaperID = strings.TrimSpace(input.PaperID)
	if input.OwnerID == "" || input.PaperID == "" {
		return Point{}, fmt.Errorf("owner_id and paper_id are required")
	}
	if !input.Kind.Valid() {
		return Point{}, fmt.Errorf("invalid point kind: %s", input.Kind)
	}
	if strings.TrimSpace(input.Text) == "" {
		return Point{}, fmt.Errorf("point text is required")
	}
	if len(input.Refs) == 0 {
		return Point{}, fmt.Errorf("point requires at least one source ref")
	}
	if _, err := b.Store.Get(ctx, input.OwnerID, input.PaperID); err != nil {
		return Point{}, fmt.Errorf("paper not found")
	}
	if input.Trust == "" {
		input.Trust = defaultPointTrust(input.Kind)
	}
	if input.Status == "" {
		input.Status = defaultPointStatus(input.Trust)
	}
	if input.Importance <= 0 {
		input.Importance = 5
	}
	if input.Confidence <= 0 {
		input.Confidence = 0.8
	}
	if input.EvidenceClass == "" {
		input.EvidenceClass = "wiki_point"
	}
	attributes := map[string]any{}
	for key, value := range input.Attributes {
		attributes[key] = value
	}
	attributes["paper_id"] = input.PaperID
	attributes["point_kind"] = string(input.Kind)
	if pointKey, ok := attributes["point_key"].(string); ok && strings.TrimSpace(pointKey) != "" {
		if err := b.supersedePointKey(ctx, input.OwnerID, input.PaperID, pointKey); err != nil {
			return Point{}, err
		}
	}
	tags := []string{TagWikiPoint, PaperTag(input.PaperID), PointTag(input.Kind)}
	entities := []memory.Entity{{Type: "paper", Value: input.PaperID}}
	record := memory.Record{
		ID:         pointRecordID(input),
		Kind:       input.Kind.MemoryKind(),
		Scope:      memory.Scope{OwnerID: input.OwnerID, ProjectID: input.ProjectID, PlanID: input.PlanID},
		Title:      truncate(input.Title, 120),
		Content:    truncate(input.Text, 800),
		Tags:       tags,
		Entities:   entities,
		Source:     memory.Source{Type: firstNonEmpty(input.SourceType, "wiki"), Ref: input.SourceRef, CreatedAt: time.Now().UTC()},
		Evidence:   memory.Evidence{Class: input.EvidenceClass, Confidence: input.Confidence},
		Refs:       input.Refs,
		Attributes: attributes,
		Importance: input.Importance,
		Confidence: input.Confidence,
		Trust:      input.Trust,
		Status:     input.Status,
	}
	if input.TaskID != "" {
		record.Entities = append(record.Entities, memory.Entity{Type: "task", Value: input.TaskID})
	}
	saved, err := b.Memory.Put(ctx, record)
	if err != nil {
		return Point{}, err
	}
	if _, err := b.RefreshPaper(ctx, input.OwnerID, input.PaperID); err != nil {
		return Point{}, err
	}
	point, _ := ParsePoint(saved)
	return point, nil
}

func defaultPointTrust(kind PointKind) memory.Trust {
	switch kind {
	case PointClaim, PointMethod, PointLimitation, PointOpenQuestion:
		return memory.TrustInferred
	case PointUserNote:
		return memory.TrustUserExplicit
	default:
		return memory.TrustVerified
	}
}

func defaultPointStatus(trust memory.Trust) memory.Status {
	if trust == memory.TrustInferred {
		return memory.StatusPending
	}
	return memory.StatusActive
}

func pointRecordID(input PointInput) string {
	pointKey, _ := input.Attributes["point_key"].(string)
	if strings.TrimSpace(pointKey) == "" {
		if input.Kind == PointUserNote {
			return "point-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
		}
		pointKey = string(input.Kind)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.OwnerID, input.PaperID, string(input.Kind), pointKey, memory.NormalizeText(input.Text),
	}, "|")))
	return "point-" + strings.ReplaceAll(pointKey, " ", "-") + "-" + hex.EncodeToString(sum[:])[:16]
}

func (b *Builder) supersedePointKey(ctx context.Context, ownerID, paperID, pointKey string) error {
	records, _, err := b.Memory.List(ctx, memory.Query{
		OwnerID: ownerID, Tags: []string{TagWikiPoint, PaperTag(paperID)}, IncludePending: true, Limit: 2000,
	})
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Status != memory.StatusActive && record.Status != memory.StatusPending {
			continue
		}
		key, _ := record.Attributes["point_key"].(string)
		if strings.TrimSpace(key) != pointKey {
			continue
		}
		status := memory.StatusSuperseded
		if _, err := b.Memory.Update(ctx, ownerID, record.ID, memory.Patch{Status: &status}); err != nil {
			return err
		}
	}
	return nil
}

// AddUserNote creates a user_note point from an explicit user edit.
func (b *Builder) AddUserNote(ctx context.Context, ownerID, projectID, paperID, text string) (Point, error) {
	return b.CreatePoint(ctx, PointInput{
		OwnerID: ownerID, ProjectID: projectID, PaperID: paperID, Kind: PointUserNote,
		Title: "用户笔记", Text: text,
		Refs:  []memory.SourceRef{{Section: "user_note", Quote: truncate(text, 500)}},
		Trust: memory.TrustUserExplicit, Status: memory.StatusActive,
		SourceType: "user", SourceRef: "user_note", Importance: 8, Confidence: 1.0,
		EvidenceClass: "user_explicit",
	})
}

// IngestPaperParse creates the pending summary point produced by paper_parse.
func (b *Builder) IngestPaperParse(ctx context.Context, ownerID, projectID string, meta models.PaperMetadata, documentID, summary string) (PaperPage, error) {
	page, err := b.EnsurePaper(ctx, ownerID, projectID, meta, documentID)
	if err != nil {
		return PaperPage{}, err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return page, nil
	}
	refs := []memory.SourceRef{}
	if documentID != "" {
		refs = append(refs, memory.SourceRef{DocumentID: documentID, Section: "summary"})
	} else {
		refs = append(refs, memory.SourceRef{Section: "paper_parse"})
	}
	_, err = b.CreatePoint(ctx, PointInput{
		OwnerID: ownerID, ProjectID: projectID, PaperID: page.PaperID, Kind: PointClaim,
		Title: "论文摘要：" + page.Title, Text: truncate(summary, 800), Refs: refs,
		Trust: memory.TrustInferred, Status: memory.StatusPending,
		SourceType: "paper_parse", SourceRef: page.PaperID, Importance: 7, Confidence: 0.6,
		EvidenceClass: "llm_inference",
		Attributes:    map[string]any{"point_key": "summary"},
	})
	if err != nil {
		return PaperPage{}, err
	}
	return b.RefreshPaper(ctx, ownerID, page.PaperID)
}

// AddResultPointFromArtifact records a metrics artifact as a result point.
func (b *Builder) AddResultPointFromArtifact(ctx context.Context, ownerID, projectID, paperID string, artifact models.Artifact, planID, taskID string) (Point, error) {
	payload := strings.TrimSpace(artifact.Value)
	if artifact.Kind != "metrics" || payload == "" || !json.Valid([]byte(payload)) {
		return Point{}, fmt.Errorf("artifact is not a valid metrics artifact")
	}
	return b.CreatePoint(ctx, PointInput{
		OwnerID: ownerID, ProjectID: projectID, PaperID: paperID, Kind: PointResult,
		Title: "实验结果：" + artifact.Key, Text: truncate(payload, 800),
		Refs:  []memory.SourceRef{{ArtifactRef: artifact.Key, TaskID: taskID, PlanID: planID}},
		Trust: memory.TrustVerified, Status: memory.StatusActive,
		SourceType: "artifact", SourceRef: artifact.Key, Importance: 8, Confidence: 0.9,
		EvidenceClass: "artifact_metrics", PlanID: planID, TaskID: taskID,
		Attributes: map[string]any{"point_key": "metric:" + artifact.Key},
	})
}

// TagPlanRecords appends wiki tags to the deterministic plan records so the
// existing memory.RecordPlan output becomes visible on the paper page without
// duplicating extraction.
func TagPlanRecords(ctx context.Context, store memory.Store, plan *models.PlanGraph, paperID string) error {
	if store == nil || plan == nil || strings.TrimSpace(paperID) == "" {
		return nil
	}
	ids := []string{"mem-plan-" + plan.ID, "mem-paper-" + plan.ID, "mem-negative-" + plan.ID, "mem-env-" + plan.ID, "mem-missing-" + plan.ID}
	for _, id := range ids {
		_ = appendTags(ctx, store, plan.OwnerID, id, []string{TagWikiPoint, PaperTag(paperID)})
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		_ = appendTags(ctx, store, plan.OwnerID, "mem-task-"+plan.ID+"-"+node.ID, []string{TagWikiPoint, PaperTag(paperID), PointTag(PointTask)})
		_ = appendTags(ctx, store, plan.OwnerID, "mem-proc-"+plan.ID+"-"+node.ID, []string{TagWikiPoint, PaperTag(paperID), PointTag(PointProcedure)})
	}
	return nil
}

// TagTaskRecord tags the task/procedure records written by memory.RecordTask.
func TagTaskRecord(ctx context.Context, store memory.Store, plan *models.PlanGraph, task *models.TaskNode, paperID string) error {
	if store == nil || plan == nil || task == nil || strings.TrimSpace(paperID) == "" {
		return nil
	}
	_ = appendTags(ctx, store, plan.OwnerID, "mem-task-"+plan.ID+"-"+task.ID, []string{TagWikiPoint, PaperTag(paperID), PointTag(PointTask)})
	_ = appendTags(ctx, store, plan.OwnerID, "mem-proc-"+plan.ID+"-"+task.ID, []string{TagWikiPoint, PaperTag(paperID), PointTag(PointProcedure)})
	return nil
}

func appendTags(ctx context.Context, store memory.Store, ownerID, recordID string, tags []string) error {
	record, err := store.Get(ctx, ownerID, recordID)
	if err != nil {
		return err
	}
	merged := append([]string{}, record.Tags...)
	changed := false
	for _, tag := range tags {
		if !hasTag(merged, tag) {
			merged = append(merged, tag)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	_, err = store.Update(ctx, ownerID, recordID, memory.Patch{Tags: &merged})
	return err
}

func hasTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

// ListPoints returns the owner-scoped points of one paper.
func (b *Builder) ListPoints(ctx context.Context, ownerID, paperID string, kind PointKind, limit, offset int) ([]Point, int, error) {
	if b.Memory == nil {
		return nil, 0, fmt.Errorf("wiki point store is not configured")
	}
	if _, err := b.Store.Get(ctx, ownerID, paperID); err != nil {
		return nil, 0, err
	}
	tags := []string{TagWikiPoint, PaperTag(paperID)}
	if kind != "" {
		tags = append(tags, PointTag(kind))
	}
	records, total, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: tags, IncludePending: true, Limit: 2000})
	if err != nil {
		return nil, 0, err
	}
	points := make([]Point, 0, len(records))
	for _, record := range records {
		if point, ok := ParsePoint(record); ok {
			points = append(points, point)
		}
	}
	sort.SliceStable(points, func(i, j int) bool {
		if points[i].Record.Importance == points[j].Record.Importance {
			return points[i].Record.UpdatedAt.After(points[j].Record.UpdatedAt)
		}
		return points[i].Record.Importance > points[j].Record.Importance
	})
	if offset >= len(points) {
		return []Point{}, total, nil
	}
	end := offset + limit
	if limit <= 0 {
		limit = 50
	}
	if end > len(points) || end <= offset {
		end = len(points)
	}
	return points[offset:end], total, nil
}

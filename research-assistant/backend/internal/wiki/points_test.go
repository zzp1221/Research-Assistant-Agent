package wiki

import (
	"context"
	"strings"
	"testing"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func newPointTestBuilder(t *testing.T) (*Builder, *memory.FileStore, PaperPage) {
	t.Helper()
	ctx := context.Background()
	mem := memory.NewMemoryStore()
	builder := NewBuilder(NewMemoryPaperStore(), mem, nil, nil, NewAliasStore(t.TempDir()))
	page, err := builder.EnsurePaper(ctx, "u1", "proj", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"}, "")
	if err != nil {
		t.Fatalf("ensure paper: %v", err)
	}
	return builder, mem, page
}

func TestCreateUserNotePoint(t *testing.T) {
	ctx := context.Background()
	builder, _, page := newPointTestBuilder(t)
	point, err := builder.AddUserNote(ctx, "u1", "proj", page.PaperID, "这篇论文的 BLEU 27.3 值得复现")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if point.Kind != PointUserNote || point.Record.Status != memory.StatusActive || point.Record.Trust != memory.TrustUserExplicit {
		t.Fatalf("unexpected point: %+v", point)
	}
	points, total, err := builder.ListPoints(ctx, "u1", page.PaperID, "", 50, 0)
	if err != nil || total != 1 || len(points) != 1 {
		t.Fatalf("list points total=%d len=%d err=%v", total, len(points), err)
	}
	if _, _, err := builder.ListPoints(ctx, "u2", page.PaperID, "", 50, 0); err == nil {
		t.Fatalf("cross-owner point list must fail")
	}
}

func TestPaperParseSummaryIsPending(t *testing.T) {
	ctx := context.Background()
	builder, _, page := newPointTestBuilder(t)
	updated, err := builder.IngestPaperParse(ctx, "u1", "proj", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"}, "", "Transformer 用纯注意力替代 RNN/CNN，在 WMT14 EN-DE 达到 27.3 BLEU。")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(updated.PointIDs) != 1 {
		t.Fatalf("point ids=%v", updated.PointIDs)
	}
	points, _, err := builder.ListPoints(ctx, "u1", page.PaperID, PointClaim, 50, 0)
	if err != nil || len(points) != 1 {
		t.Fatalf("claim points len=%d err=%v", len(points), err)
	}
	if points[0].Record.Status != memory.StatusPending || points[0].Record.Trust != memory.TrustInferred {
		t.Fatalf("paper_parse point must be pending/inferred: %+v", points[0].Record)
	}
	if injectablePoint(points[0]) {
		t.Fatalf("pending point must not be injectable")
	}
}

func TestPointKeySupersede(t *testing.T) {
	ctx := context.Background()
	builder, _, page := newPointTestBuilder(t)
	refs := []memory.SourceRef{{DocumentID: "doc-1", Section: "summary"}}
	first, err := builder.CreatePoint(ctx, PointInput{
		OwnerID: "u1", ProjectID: "proj", PaperID: page.PaperID, Kind: PointClaim,
		Title: "摘要 v1", Text: "第一版摘要", Refs: refs, Attributes: map[string]any{"point_key": "summary"},
	})
	if err != nil {
		t.Fatalf("first point: %v", err)
	}
	second, err := builder.CreatePoint(ctx, PointInput{
		OwnerID: "u1", ProjectID: "proj", PaperID: page.PaperID, Kind: PointClaim,
		Title: "摘要 v2", Text: "第二版摘要", Refs: refs, Attributes: map[string]any{"point_key": "summary"},
	})
	if err != nil {
		t.Fatalf("second point: %v", err)
	}
	firstRecord, err := builder.Memory.Get(ctx, "u1", first.Record.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstRecord.Status != memory.StatusSuperseded {
		t.Fatalf("old point status=%q, want superseded", firstRecord.Status)
	}
	if second.Record.ID == first.Record.ID {
		t.Fatalf("supersede must allocate a new point id")
	}
}

func TestResultPointFromMetricsArtifact(t *testing.T) {
	ctx := context.Background()
	builder, _, page := newPointTestBuilder(t)
	point, err := builder.AddResultPointFromArtifact(ctx, "u1", "proj", page.PaperID, models.Artifact{Key: "metrics", Kind: "metrics", Value: `{"bleu":27.3,"dataset":"WMT14"}`}, "plan-1", "task-1")
	if err != nil {
		t.Fatalf("result point: %v", err)
	}
	if point.Kind != PointResult || point.Record.Status != memory.StatusActive {
		t.Fatalf("unexpected result point: %+v", point)
	}
	if _, err := builder.AddResultPointFromArtifact(ctx, "u1", "proj", page.PaperID, models.Artifact{Key: "x", Kind: "text", Value: "not json"}, "p", "t"); err == nil {
		t.Fatalf("non-metrics artifact must be rejected")
	}
}

func TestPointRequiresEvidence(t *testing.T) {
	ctx := context.Background()
	builder, _, page := newPointTestBuilder(t)
	if _, err := builder.CreatePoint(ctx, PointInput{OwnerID: "u1", PaperID: page.PaperID, Kind: PointClaim, Title: "x", Text: "no refs"}); err == nil || !strings.Contains(err.Error(), "source ref") {
		t.Fatalf("expected missing-ref error, got %v", err)
	}
}

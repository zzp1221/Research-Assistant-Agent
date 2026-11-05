package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RecordDocument stores a bounded document map (title/page count/section
// titles) without copying the full text. Missing sections are explicitly
// marked pending_review so the next paper_parse task can fall back to page-range
// retrieval instead of repeating the same heading-detection failure.
func RecordDocument(ctx context.Context, store Store, ownerID, projectID, sessionID, planID, documentID, title string, pageCount int, sections []string, status string) error {
	if store == nil || strings.TrimSpace(ownerID) == "" || strings.TrimSpace(documentID) == "" {
		return nil
	}
	now := time.Now().UTC()
	cleaned := make([]string, 0, len(sections))
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section != "" {
			cleaned = append(cleaned, section)
		}
		if len(cleaned) >= 20 {
			break
		}
	}
	recordStatus := StatusActive
	content := fmt.Sprintf("文档 %s；页数：%d；状态：%s；章节（%d）：%s", documentID, pageCount, status, len(cleaned), strings.Join(cleaned, " | "))
	if len(cleaned) == 0 {
		recordStatus = StatusPending
		content += "；section_detection_failed：后续使用 page-range 检索"
	}
	rec := Record{
		ID: "mem-doc-" + documentID, Kind: KindDocument,
		Scope: documentExperienceScope(ownerID, projectID, sessionID),
		Title: truncateRunes("文档："+title, 120), Content: truncateRunes(content, 800),
		Tags: []string{"document", "sections"}, Entities: []Entity{{Type: "document", Value: documentID}},
		Source:     Source{Type: "document", Ref: documentID, CreatedAt: now},
		Refs:       []SourceRef{{DocumentID: documentID, PlanID: planID, SessionID: sessionID}},
		Evidence:   Evidence{Class: "paper_derived", Confidence: 0.9},
		Validity:   Validity{RecordedAt: now},
		Importance: 8, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: recordStatus,
	}
	_, err := store.Put(ctx, rec)
	return err
}

func documentExperienceScope(ownerID, projectID, sessionID string) Scope {
	scope := Scope{OwnerID: ownerID, ProjectID: projectID}
	if projectID == "" {
		scope.SessionID = sessionID
	}
	return scope
}

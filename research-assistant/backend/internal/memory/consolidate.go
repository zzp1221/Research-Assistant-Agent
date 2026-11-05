package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ConsolidationReport summarizes one consolidation pass.
type ConsolidationReport struct {
	Scanned     int `json:"scanned"`
	Archived    int `json:"archived"`
	Reflections int `json:"reflections"`
}

// Consolidator performs deterministic sleep-time maintenance: TTL/archival and
// reflection generation. It never calls an LLM, so it can run in the background
// without blocking a request.
type Consolidator struct {
	mu            sync.Mutex
	store         Store
	retentionDays int
	settings      *SettingsStore
}

func NewConsolidator(store Store, settings ...*SettingsStore) *Consolidator {
	c := &Consolidator{store: store, retentionDays: 180}
	if len(settings) > 0 {
		c.settings = settings[0]
	}
	return c
}

// RunOnce scans one owner and applies retention plus reflection rules.
func (c *Consolidator) RunOnce(ctx context.Context, ownerID string) (ConsolidationReport, error) {
	if c == nil || c.store == nil {
		return ConsolidationReport{}, fmt.Errorf("memory consolidator is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	report := ConsolidationReport{}
	records, _, err := c.store.List(ctx, Query{
		OwnerID:   ownerID,
		Statuses:  []Status{StatusActive, StatusPending, StatusSuperseded, StatusArchived},
		AllScopes: true,
		Limit:     100000,
	})
	if err != nil {
		return report, err
	}
	report.Scanned = len(records)
	now := time.Now().UTC()
	retention := c.retentionDays
	if c.settings != nil {
		retention = c.settings.Get(ownerID).RetentionDays
	}
	groups := map[string][]Record{}
	for _, rec := range records {
		if rec.Status == StatusActive && !rec.Pinned {
			ttl := retentionDaysFor(rec.Kind)
			// Owner retention caps the per-kind defaults. Profiles and pinned
			// records remain exempt; pending candidates are never auto-promoted.
			if ttl > 0 && retention > 0 && retention < ttl {
				ttl = retention
			}
			if ttl > 0 && now.Sub(rec.UpdatedAt) > time.Duration(ttl)*24*time.Hour {
				status := StatusArchived
				if _, err := c.store.Update(ctx, ownerID, rec.ID, Patch{Status: &status}); err == nil {
					report.Archived++
				}
			}
		}
		if (rec.Kind == KindEpisode || rec.Kind == KindNegative) && (rec.Status == StatusActive || rec.Status == StatusArchived) && rec.Trust != TrustUntrusted {
			key := rec.Scope.OwnerID + "|" + rec.Scope.ProjectID
			if rec.Scope.ProjectID == "" {
				key += "|" + rec.Scope.SessionID + "|" + rec.Scope.PlanID + "|" + rec.Scope.AgentID
			}
			groups[key] = append(groups[key], rec)
		}
	}
	for key, items := range groups {
		if len(items) < 5 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		derived := make([]string, 0, 3)
		titles := make([]string, 0, 3)
		for _, item := range items {
			if len(derived) >= 3 {
				break
			}
			derived = append(derived, item.ID)
			titles = append(titles, truncateRunes(item.Title, 60))
		}
		scope := items[0].Scope
		if scope.ProjectID != "" {
			scope = Scope{OwnerID: ownerID, ProjectID: scope.ProjectID}
		}
		var content strings.Builder
		var refs []SourceRef
		for _, item := range items[:3] {
			fmt.Fprintf(&content, "条件与经验 [%s]：%s\n", item.ID, truncateRunes(item.Content, 600))
			refs = append(refs, item.Refs...)
		}
		reflection := Record{
			ID: "mem-reflection-" + strings.ReplaceAll(key, "|", "-"), Kind: KindReflection, Scope: scope,
			Title:   "经验归纳：" + strings.Join(titles, "；"),
			Content: content.String(), Refs: refs,
			Tags: []string{"reflection", "consolidated"}, DerivedFrom: derived,
			Source:   Source{Type: "consolidation", Ref: key, CreatedAt: now},
			Evidence: Evidence{Class: "llm_inference", Confidence: 0.6}, Validity: Validity{RecordedAt: now},
			Importance: 7, Confidence: 0.6, Trust: TrustInferred, Sensitivity: "private", Status: StatusPending,
		}
		if _, err := c.store.Put(ctx, reflection); err == nil {
			report.Reflections++
		}
	}
	return report, nil
}

func retentionDaysFor(kind Kind) int {
	switch kind {
	case KindWorking:
		return 1
	case KindEpisode, KindConstraint:
		return 90
	case KindFact, KindNegative:
		return 180
	case KindProcedure, KindDocument, KindReflection:
		return 365
	case KindProfile:
		return 0
	default:
		return 180
	}
}

// Start runs consolidation on a fixed interval until ctx is canceled.
func (c *Consolidator) Start(ctx context.Context, interval time.Duration) {
	if c == nil || c.store == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, ownerID := range ownersOf(c.store) {
					_, _ = c.RunOnce(ctx, ownerID)
				}
			}
		}
	}()
}

func ownersOf(store Store) []string {
	type ownerLister interface{ Owners() []string }
	if lister, ok := store.(ownerLister); ok {
		return lister.Owners()
	}
	return nil
}

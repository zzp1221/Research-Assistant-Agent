package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/models"
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
		if (rec.Kind == KindEpisode || rec.Kind == KindNegative || rec.Kind == KindProcedure) && (rec.Status == StatusActive || rec.Status == StatusArchived || (rec.Kind == KindProcedure && rec.Status == StatusPending)) && rec.Trust != TrustUntrusted {
			// Never broaden a session/plan/agent record into project visibility.
			scopeKey, _ := json.Marshal(rec.Scope)
			key := string(scopeKey)
			groups[key] = append(groups[key], rec)
		}
	}
	for key, items := range groups {
		if len(items) < 5 {
			continue
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		// Keep contrasting outcomes before filling with recent cases. This is a
		// reviewable case compilation, never an automatically inferred method.
		chosen := selectReflectionCases(items, 8)
		derived := make([]string, 0, len(chosen))
		titles := make([]string, 0, 3)
		scope := items[0].Scope
		var content strings.Builder
		var refs []SourceRef
		content.WriteString("案例汇编（待审）：下列条件、动作与结果来自原始记录；未知条件保持未知，不据此宣称通用方法。成功与失败必须分别核对环境、协议和原始证据。\n")
		cases := make([]ReflectionCase, 0, len(chosen))
		independent := map[string]bool{}
		for _, item := range chosen {
			derived = append(derived, item.ID)
			if len(titles) < 3 {
				titles = append(titles, truncateRunes(item.Title, 60))
			}
			entry := reflectionCase(item)
			cases = append(cases, entry)
			if entry.IndependentExecution != "" {
				independent[entry.IndependentExecution] = true
			}
			encoded, _ := json.Marshal(entry)
			fmt.Fprintf(&content, "%s\n", encoded)
			refs = append(refs, item.Refs...)
		}
		// References live at the top level to respect bounded metadata depth.
		caseMetadata := make([]map[string]any, 0, len(cases))
		for _, entry := range cases {
			caseMetadata = append(caseMetadata, map[string]any{"memory_id": entry.MemoryID, "problem": entry.Problem, "action": entry.Action, "environment_hash": entry.Environment, "protocol_hash": entry.Protocol, "outcome": entry.Outcome, "independent_execution": entry.IndependentExecution})
		}
		evidence := map[string]any{"mode": "case_compilation", "cases": caseMetadata, "independent_executions": len(independent), "generalization": "not_established", "source_records": len(items), "omitted_records": len(items) - len(chosen)}
		reflection := Record{
			ID: "mem-reflection-" + ContentHash(KindReflection, scope, key, "case_compilation")[7:], Kind: KindReflection, Scope: scope,
			Title:   "案例汇编：" + strings.Join(titles, "；"),
			Content: content.String(), Refs: refs,
			Tags: []string{"reflection", "consolidated", "case_compilation"}, DerivedFrom: derived,
			Attributes: map[string]any{"reflection_evidence": evidence},
			Source:     Source{Type: "consolidation", Ref: key, CreatedAt: now},
			Evidence:   Evidence{Class: "deterministic_compilation", Confidence: 0.6}, Validity: Validity{RecordedAt: now},
			Importance: 7, Confidence: 0.6, Trust: TrustInferred, Sensitivity: "private", Status: StatusPending,
		}
		if existing, err := c.store.Get(ctx, ownerID, reflection.ID); err == nil && existing.Content == reflection.Content {
			continue
		}
		if _, err := c.store.Put(ctx, reflection); err != nil {
			return report, fmt.Errorf("persist reflection: %w", err)
		}
		report.Reflections++
	}
	return report, nil
}

// ReflectionCase keeps unknown coordinates explicit. Independent evidence is
// counted only when an actual execution identity and original artifacts exist.
type ReflectionCase struct {
	MemoryID             string      `json:"memory_id"`
	Problem              string      `json:"problem"`
	Action               string      `json:"action"`
	Environment          string      `json:"environment_hash"`
	Protocol             string      `json:"protocol_hash"`
	Outcome              string      `json:"outcome"`
	IndependentExecution string      `json:"independent_execution,omitempty"`
	Quote                string      `json:"quote"`
	QuoteTruncated       bool        `json:"quote_truncated"`
	Refs                 []SourceRef `json:"refs,omitempty"`
}

func reflectionCase(rec Record) ReflectionCase {
	field := func(key string) string {
		if value, ok := rec.Attributes[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		return "unknown"
	}
	result := ReflectionCase{MemoryID: rec.ID, Problem: field("problem"), Action: field("action"), Environment: field("environment_hash"), Protocol: field("protocol_hash"), Outcome: field("outcome"), Quote: truncateRunes(rec.Content, 400), QuoteTruncated: len([]rune(rec.Content)) > 400, Refs: rec.Refs}
	if rec.Source.Type == "action_ledger" && rec.Evidence.Class == "execution_derived" {
		var action models.ActionLedgerEntry
		if start := strings.Index(rec.Content, "{"); start >= 0 && json.Unmarshal([]byte(rec.Content[start:]), &action) == nil {
			result.Action = action.Action
			result.Problem = "before_state=" + action.BeforeStateHash
			result.Environment, result.Protocol = action.EnvironmentHash, action.ProtocolHash
			if action.Verified && action.Kind == "repair" && action.ExecutionID != "" && action.BeforeStateHash != "" && action.AfterStateHash != "" && action.BeforeStateHash != action.AfterStateHash && len(action.BeforeEvidence) > 0 && len(action.AfterEvidence) > 0 && len(action.ArtifactRefs) > 0 {
				result.IndependentExecution = action.ExecutionID
				result.Outcome = "verified_single_case"
			}
		}
	}
	if rec.Kind == KindNegative {
		result.Outcome = "negative"
	}
	if rec.Evidence.Class == "execution_derived" && rec.Trust == TrustVerified && field("execution_id") != "unknown" {
		for _, ref := range rec.Refs {
			if ref.ArtifactRef != "" {
				result.IndependentExecution = field("execution_id")
				break
			}
		}
	}
	return result
}

func selectReflectionCases(items []Record, limit int) []Record {
	chosen := make([]Record, 0, min(limit, len(items)))
	used := map[string]bool{}
	conditions := map[string]bool{}
	// Keep one case from every explicit condition/outcome combination first.
	for _, item := range items {
		entry := reflectionCase(item)
		key := entry.Problem + "|" + entry.Action + "|" + entry.Environment + "|" + entry.Protocol + "|" + entry.Outcome
		if conditions[key] || len(chosen) >= limit {
			continue
		}
		conditions[key], used[item.ID] = true, true
		chosen = append(chosen, item)
	}
	for _, item := range items {
		if !used[item.ID] && len(chosen) < limit {
			chosen = append(chosen, item)
		}
	}
	return chosen
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

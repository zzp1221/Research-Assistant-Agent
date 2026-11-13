package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// cloneSession returns a deep copy so a caller can mutate its result without
// reaching into the live store. The JSON round trip decouples every slice,
// including ChatTurn.References; cloneChatTurnReferences covers the paths that
// only copy turn structs, where the reference slice would otherwise be shared.
func cloneSession(session ChatSession) ChatSession {
	raw, _ := json.Marshal(session)
	var copied ChatSession
	_ = json.Unmarshal(raw, &copied)
	return copied
}

// cloneChatTurnReferences detaches one turn's reference slice from its source.
func cloneChatTurnReferences(turn *ChatTurn) {
	if turn == nil || turn.References == nil {
		return
	}
	references := make([]ChatReference, len(turn.References))
	copy(references, turn.References)
	turn.References = references
}

func chatRecord(session ChatSession, turn ChatTurn, part int, text string) (Record, error) {
	// Check the entire original before selecting a chunk, including secrets that
	// straddle the boundaries between overlapping chunks.
	for _, pattern := range secretPatterns {
		if pattern.MatchString(turn.Content) {
			return Record{}, fmt.Errorf("chat source contains a secret")
		}
	}
	if matched, _ := ScanInjection(turn.Content); matched || IsHiddenText(turn.Content) || hasLargeBase64(turn.Content) {
		return Record{}, fmt.Errorf("chat source is not injectable")
	}
	rec := Record{
		ID: fmt.Sprintf("chat:%s:%s:%d", session.ID, turn.ID, part), Kind: KindEpisode,
		Scope: Scope{OwnerID: session.OwnerID, ProjectID: session.ProjectID, SessionID: session.ID},
		Title: deriveChatTitle(text), Content: text,
		Source: Source{Type: "chat", Ref: turn.ID, CreatedAt: turn.CreatedAt},
		Refs:   []SourceRef{{SessionID: session.ID, MessageID: turn.ID, Quote: truncateRunes(text, 480)}},
		Trust:  TrustUserExplicit, Status: StatusActive, Importance: 7,
		Evidence: Evidence{Class: "user_statement", Confidence: 1}, CreatedAt: turn.CreatedAt,
	}
	rec.Normalize(turn.CreatedAt)
	if err := ValidateRecord(&rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (s *ChatStore) indexTurnLocked(session ChatSession, turn ChatTurn) {
	// Assistant guesses cannot become historical user facts. Original messages
	// remain intact; overlapping chunks cover values anywhere in a long message.
	if turn.Role != "user" {
		return
	}
	runes := []rune(turn.Content)
	for start, part := 0, 0; start < len(runes); part++ {
		end := start + 600
		if end > len(runes) {
			end = len(runes)
		}
		rec, err := chatRecord(session, turn, part, string(runes[start:end]))
		if err == nil && rec.Trust != TrustUntrusted {
			s.chunks[rec.ID] = rec
			s.index.Add(rec)
		}
		if end == len(runes) {
			break
		}
		start = end - 80
	}
}

// ContextForQuery combines a durable incremental summary with query-selected
// original user messages. The recall block has its own explicit token budget.
func (s *ChatStore) ContextForQuery(ownerID, projectID, sessionID, text string, historyBudget, maxTurns, recallBudget int) (string, []ChatTurn, Context, error) {
	s.mu.RLock()
	session, exists := s.data[sessionID]
	authorized := exists && session.OwnerID == ownerID && session.ProjectID == projectID
	s.mu.RUnlock()
	if !authorized {
		return "", nil, Context{}, nil
	}
	summary, recent, err := s.contextTurns(ownerID, sessionID, historyBudget, maxTurns)
	if err != nil || recallBudget <= 0 {
		return summary, recent, Context{}, err
	}
	return summary, recent, s.RecallForQuery(ownerID, projectID, sessionID, text, recallBudget), nil
}

// RecallForQuery retrieves original sources without changing the active model window.
func (s *ChatStore) RecallForQuery(ownerID, projectID, sessionID, text string, recallBudget int, excludeMessageIDs ...string) Context {
	if recallBudget <= 0 {
		return Context{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := Query{OwnerID: ownerID, ProjectID: projectID, SessionID: sessionID}
	visible := map[string]struct{}{}
	excluded := map[string]bool{}
	for _, id := range excludeMessageIDs {
		excluded[id] = true
	}
	for id, rec := range s.chunks {
		if matchQuery(rec, q) && !excluded[rec.Source.Ref] {
			visible[id] = struct{}{}
		}
	}
	hits := s.index.SearchBM25In(text, 6, visible)
	records := make([]Record, 0, len(hits))
	for _, hit := range hits {
		records = append(records, s.chunks[hit.ID])
	}
	// Add the latest research state after query hits. It stays within the same
	// budget, and each item still carries its original message identity.
	if session, ok := s.data[sessionID]; ok && session.OwnerID == ownerID && session.ProjectID == projectID {
		state := map[string]ChatTurn{}
		for _, turn := range session.Turns {
			if turn.Role == "user" && !excluded[turn.ID] {
				if role := researchRole(turn.Content); role != "" {
					state[role] = turn
				}
			}
		}
		for _, role := range sortedStateRoles(state) {
			turn := state[role]
			rec, err := chatRecord(session, turn, -1, truncateRunes(turn.Content, 600))
			if err == nil && rec.Trust != TrustUntrusted {
				records = append(records, rec)
			}
		}
	}
	selected, rendered, tokens, truncated := renderWithBudget(records, recallBudget)
	ctx := Context{Retrieved: selected, RetrievedText: rendered, Tokens: tokens,
		Report: RetrieveReport{Candidates: len(hits), Selected: len(selected), RetrievedTokens: tokens, Truncated: truncated}}
	for _, rec := range selected {
		ctx.Citations = append(ctx.Citations, Citation{MemoryID: rec.ID, Title: rec.Title, Kind: rec.Kind,
			Source: "chat:" + rec.Source.Ref, Trust: rec.Trust, Quote: rec.Content, SessionID: sessionID, MessageID: rec.Source.Ref})
	}
	return ctx
}

// WorkingState returns the most recent evidence for each research role, keeping
// the original message identity so a handoff can recover the full context.
func (s *ChatStore) WorkingState(ownerID, sessionID string) map[string]ChatTurn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := map[string]ChatTurn{}
	if session, ok := s.data[sessionID]; ok && session.OwnerID == ownerID {
		for _, turn := range session.Turns {
			if turn.Role != "user" {
				continue
			}
			if role := researchRole(turn.Content); role != "" {
				// The returned turn must not alias the live store's reference
				// slice; callers may edit the handoff before reuse.
				cloneChatTurnReferences(&turn)
				state[role] = turn
			}
		}
	}
	return state
}

func researchRole(text string) string {
	text = strings.ToLower(text)
	roles := []struct {
		role  string
		terms []string
	}{
		{"correction", []string{"纠正", "更正", "改为", "correction"}},
		{"negative", []string{"负结果", "失败", "退化", "failed", "negative result"}},
		{"budget", []string{"显存", "资源约束", "预算", "gpu", "budget"}},
		{"dataset", []string{"数据划分", "测试集", "数据版本", "dataset", "split"}},
		{"seed", []string{"随机种子", "seed"}},
		{"result", []string{"实测", "基线", "结果", "ndcg", "bleu", "metric", "baseline"}},
		{"todo", []string{"下一步", "待办", "交接", "next step"}},
		{"goal", []string{"预注册", "研究目标", "研究要求", "research goal"}},
	}
	for _, group := range roles {
		for _, term := range group.terms {
			if strings.Contains(text, term) {
				return group.role
			}
		}
	}
	return ""
}

func isResearchStatement(text string) bool {
	for _, marker := range []string{"记住", "remember", "禁止", "固定", "确认结果", "实测", "纠正", "更正", "负结果"} {
		if strings.Contains(strings.ToLower(text), marker) {
			return true
		}
	}
	return false
}

func sortedStateRoles(state map[string]ChatTurn) []string {
	roles := make([]string, 0, len(state))
	for role := range state {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

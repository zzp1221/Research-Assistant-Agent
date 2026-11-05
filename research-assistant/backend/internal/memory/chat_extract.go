package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var explicitChatKey = regexp.MustCompile(`(?i)fact_key\s*[:=]\s*([a-z0-9_.:/-]+)`)

// ChatFacts promotes only user assertions with an explicit research/remember
// signal. It preserves the source message, and never reads assistant output.
func ChatFacts(session ChatSession, turn ChatTurn) []Record {
	if turn.Role != "user" || !isResearchStatement(turn.Content) {
		return nil
	}
	return chatFactsFromQuotes(session, turn, []string{turn.Content}, false)
}

// ExtractedChatFacts accepts exact user excerpts only. LLM-selected material is
// a review candidate even when the text is verbatim; extraction is not proof.
func ExtractedChatFacts(session ChatSession, turn ChatTurn, raw string) []Record {
	if turn.Role != "user" {
		return nil
	}
	var payload struct {
		Quotes []string `json:"quotes"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload) != nil {
		return nil
	}
	if len(payload.Quotes) > 4 {
		payload.Quotes = payload.Quotes[:4]
	}
	return chatFactsFromQuotes(session, turn, payload.Quotes, true)
}

func chatFactsFromQuotes(session ChatSession, turn ChatTurn, quotes []string, inferred bool) []Record {
	// Validate the whole message before chunking so a secret or injection cannot
	// be split across chunk boundaries and accidentally promoted.
	probe := Record{Kind: KindFact, Scope: Scope{OwnerID: session.OwnerID}, Content: turn.Content, Trust: TrustUserExplicit}
	probe.Normalize(turn.CreatedAt)
	if ValidateRecord(&probe) != nil || probe.Trust == TrustUntrusted {
		return nil
	}
	var records []Record
	for n, quote := range quotes {
		quote = strings.TrimSpace(quote)
		if quote == "" || !strings.Contains(turn.Content, quote) {
			continue
		}
		runes := []rune(quote)
		for start, part := 0, 0; start < len(runes); part++ {
			end := start + 1200
			if end > len(runes) {
				end = len(runes)
			}
			content := string(runes[start:end])
			scope := Scope{OwnerID: session.OwnerID, ProjectID: session.ProjectID}
			if scope.ProjectID == "" {
				scope.SessionID = session.ID
			}
			rec := Record{ID: fmt.Sprintf("mem-chat-%s-%d-%d", turn.ID, n, part), Kind: KindFact,
				Scope: scope, Title: deriveChatTitle(content), Content: content,
				Source: Source{Type: "chat", Ref: turn.ID, CreatedAt: turn.CreatedAt},
				Refs:   []SourceRef{{SessionID: session.ID, MessageID: turn.ID, Quote: truncateRunes(content, 480)}},
				Trust:  TrustUserExplicit, Status: StatusActive, Importance: 8,
				Evidence: Evidence{Class: "user_statement", Confidence: 1}, CreatedAt: turn.CreatedAt,
				Attributes: map[string]any{"extraction": "user_rule", "research_role": researchRole(content)},
			}
			if key := explicitChatKey.FindStringSubmatch(content); len(key) == 2 {
				rec.Attributes["fact_key"] = key[1]
			}
			if researchRole(content) == "correction" && rec.Attributes["fact_key"] == nil {
				rec.Status, rec.Conflict = StatusPending, true
				rec.Attributes["review_reason"] = "correction requires an identified fact"
			}
			if inferred {
				rec.Trust, rec.Status = TrustInferred, StatusPending
				rec.Attributes["extraction"] = "llm_excerpt"
			}
			rec.Normalize(turn.CreatedAt)
			if ValidateRecord(&rec) == nil && rec.Trust != TrustUntrusted {
				records = append(records, rec)
			}
			if end == len(runes) {
				break
			}
			start = end - 80
		}
	}
	return records
}

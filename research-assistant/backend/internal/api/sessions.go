package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

type createSessionRequest struct {
	ProjectID string `json:"project_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// registerSessionRoutes adds the server-owned session lifecycle. The existing
// GET/DELETE /api/sessions handlers are enhanced in plan_runtime.go so they can
// merge plan counts and chat turns.
func registerSessionRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/sessions", createServerSession)
	apiGroup.POST("/sessions/:id/activate", activateServerSession)
}

func createServerSession(c *gin.Context) {
	var payload createSessionRequest
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ownerID := resolveUserID(c)
	store := SessionStore()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session store is not configured"})
		return
	}
	created, err := store.Create(c.Request.Context(), ownerID, payload.ProjectID, payload.Title, payload.Kind)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	issueSessionCookie(c, created.ID)
	c.JSON(http.StatusCreated, gin.H{"session_id": created.ID, "title": created.Title, "kind": created.Kind})
}

func activateServerSession(c *gin.Context) {
	ownerID := resolveUserID(c)
	store := SessionStore()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session store is not configured"})
		return
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	found, err := store.Get(c.Request.Context(), ownerID, sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	_ = store.Touch(c.Request.Context(), ownerID, sessionID)
	issueSessionCookie(c, found.ID)
	c.JSON(http.StatusOK, gin.H{"session_id": found.ID, "title": found.Title, "kind": found.Kind})
}

// sessionSummary is the merged view returned by GET /api/sessions.
type sessionSummary struct {
	SessionID  string `json:"session_id"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	PlanCount  int    `json:"plan_count"`
	ChatTurns  int    `json:"chat_turns"`
	LastActive string `json:"last_active"`
}

func buildSessionSummaries(c *gin.Context, plans []*models.PlanGraph) []sessionSummary {
	ownerID := resolveUserID(c)
	byID := map[string]*sessionSummary{}
	ensure := func(id string) *sessionSummary {
		item := byID[id]
		if item == nil {
			item = &sessionSummary{SessionID: id}
			byID[id] = item
		}
		return item
	}
	if store := SessionStore(); store != nil {
		if list, err := store.List(c.Request.Context(), ownerID); err == nil {
			for _, item := range list {
				entry := ensure(item.ID)
				entry.Title = item.Title
				entry.Kind = item.Kind
				entry.LastActive = item.LastActiveAt.Format(time.RFC3339)
			}
		}
	}
	for _, plan := range plans {
		if plan == nil || plan.OwnerID != ownerID || strings.TrimSpace(plan.SessionID) == "" {
			continue
		}
		entry := ensure(plan.SessionID)
		entry.PlanCount++
		if plan.UpdatedAt.Format(time.RFC3339) > entry.LastActive {
			entry.LastActive = plan.UpdatedAt.Format(time.RFC3339)
		}
		if entry.Kind == "" {
			entry.Kind = "plan"
		}
	}
	if chats := ChatStore(); chats != nil {
		for _, chat := range chats.List(ownerID) {
			entry := ensure(chat.ID)
			entry.ChatTurns = len(chat.Turns)
			if entry.Title == "" {
				entry.Title = chat.Title
			}
			if entry.Kind == "" || entry.Kind == "plan" {
				if entry.PlanCount > 0 {
					entry.Kind = "mixed"
				} else {
					entry.Kind = "chat"
				}
			}
			if updated := chat.UpdatedAt.Format(time.RFC3339); updated > entry.LastActive {
				entry.LastActive = updated
			}
		}
	}
	items := make([]sessionSummary, 0, len(byID))
	for _, item := range byID {
		if item.Kind == "" {
			item.Kind = "plan"
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActive > items[j].LastActive })
	return items
}

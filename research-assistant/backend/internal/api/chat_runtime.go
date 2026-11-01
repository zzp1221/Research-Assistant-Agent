package api

import (
	"net/http"
	"strings"

	"research-assistant-backend/internal/agent"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// handleChat is the multi-turn chat path. It resolves the server-owned session,
// loads bounded history + summary, injects core/retrieved memory, and appends
// both turns back to the durable chat store.
func handleChat(c *gin.Context, chatAgent *agent.ChatAgent) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	var payload ChatPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	message := strings.TrimSpace(payload.Message)
	if message == "" && len(payload.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required", "code": "message_required"})
		return
	}
	if message == "" {
		for i := len(payload.Messages) - 1; i >= 0; i-- {
			if strings.EqualFold(strings.TrimSpace(payload.Messages[i].Role), "user") {
				message = strings.TrimSpace(payload.Messages[i].Content)
				break
			}
		}
	}
	if message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required", "code": "message_required"})
		return
	}

	ownerID := resolveUserID(c)
	projectID := strings.TrimSpace(payload.ProjectID)
	sessionID := strings.TrimSpace(payload.SessionID)
	sessionStore := SessionStore()
	if sessionID != "" {
		if sessionStore != nil {
			if _, err := sessionStore.Ensure(c.Request.Context(), sessionID, ownerID, projectID, "chat"); err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "session not found", "code": "session_not_found"})
				return
			}
		}
	} else {
		sessionID = resolveSessionID(c)
		if sessionID == "" {
			sessionID = "chat-" + uuid.NewString()
		}
		if sessionStore != nil {
			_, _ = sessionStore.Ensure(c.Request.Context(), sessionID, ownerID, projectID, "chat")
		}
	}

	chatStore := ChatStore()
	var summary string
	history := make([]memory.ChatTurn, 0)
	if chatStore != nil {
		if len(payload.Messages) > 0 {
			for _, msg := range payload.Messages {
				role := strings.ToLower(strings.TrimSpace(msg.Role))
				content := strings.TrimSpace(msg.Content)
				if content == "" || (role != "user" && role != "assistant") {
					continue
				}
				history = append(history, memory.ChatTurn{Role: role, Content: content})
			}
		} else {
			summary, history = chatStore.ContextTurns(ownerID, sessionID, 32000, 20)
		}
	}

	var memCtx memory.Context
	useMemory := true
	if payload.UseMemory != nil {
		useMemory = *payload.UseMemory
	}
	if useMemory && MemoryEnabled() {
		settings := memory.DefaultSettings()
		if MemorySettings() != nil {
			settings = MemorySettings().Get(ownerID)
		}
		if settings.Enabled && settings.Mode != "off" && settings.Mode != "shadow" {
			recent := make([]string, 0, 3)
			for i := len(history) - 1; i >= 0 && len(recent) < 3; i-- {
				if history[i].Role == "user" {
					recent = append(recent, history[i].Content)
				}
			}
			q := memory.Query{
				OwnerID: ownerID, ProjectID: projectID, SessionID: sessionID,
				Text: memory.BuildQuery(message, recent, 2000), Limit: settings.RetrievalTopK,
			}
			if store := MemoryStore(); store != nil {
				if got, err := store.Retrieve(c.Request.Context(), q, settings.CoreBudgetTokens, settings.RetrievedBudgetTokens); err == nil {
					memCtx = got
				}
			}
		}
	}

	messages := make([]*schema.Message, 0, len(history)+4)
	messages = append(messages, &schema.Message{Role: schema.System, Content: prompts.ChatSystemPrompt + "\n\n" + prompts.ChatMemoryInstruction})
	if memCtx.CoreText != "" {
		messages = append(messages, &schema.Message{Role: schema.System, Content: "[core memory]\n" + memCtx.CoreText})
	}
	if strings.TrimSpace(summary) != "" {
		messages = append(messages, &schema.Message{Role: schema.System, Content: summary})
	}
	for _, turn := range history {
		role := schema.User
		if turn.Role == "assistant" {
			role = schema.Assistant
		}
		messages = append(messages, &schema.Message{Role: role, Content: turn.Content})
	}
	if memCtx.RetrievedText != "" {
		messages = append(messages, &schema.Message{Role: schema.System, Content: "[retrieved memory]\n" + memCtx.RetrievedText})
	}
	messages = append(messages, &schema.Message{Role: schema.User, Content: message})

	ctx := modelresolver.WithOwner(c.Request.Context(), ownerID)
	response, err := chatAgent.AnswerWithMessages(ctx, messages)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if chatStore != nil {
		if len(payload.Messages) > 0 {
			if existing, ok := chatStore.Get(ownerID, sessionID); !ok || len(existing.Turns) == 0 {
				for _, msg := range payload.Messages {
					role := strings.ToLower(strings.TrimSpace(msg.Role))
					if role != "user" && role != "assistant" {
						continue
					}
					_, _ = chatStore.Append(ownerID, sessionID, projectID, memory.ChatTurn{Role: role, Content: msg.Content})
				}
			}
		}
		_, _ = chatStore.Append(ownerID, sessionID, projectID, memory.ChatTurn{Role: "user", Content: message})
		_, _ = chatStore.Append(ownerID, sessionID, projectID, memory.ChatTurn{Role: "assistant", Content: response})
	}
	if sessionStore != nil {
		_ = sessionStore.Touch(c.Request.Context(), ownerID, sessionID)
	}

	c.JSON(http.StatusOK, gin.H{
		"response":   response,
		"session_id": sessionID,
		"citations":  memCtx.Citations,
	})
}

package api

import (
	"log"
	"path/filepath"
	"sync"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/session"
)

var sessionRuntimeOnce struct {
	sync.Once
	sessions session.Store
	chats    *memory.ChatStore
}

// SessionStore returns the process-wide server session store.
func SessionStore() session.Store {
	sessionRuntimeOnce.Do(initSessionRuntime)
	return sessionRuntimeOnce.sessions
}

// ChatStore returns the process-wide chat history store.
func ChatStore() *memory.ChatStore {
	sessionRuntimeOnce.Do(initSessionRuntime)
	return sessionRuntimeOnce.chats
}

func initSessionRuntime() {
	dir := MemoryDirectory()
	store, err := session.NewFileStore(filepath.Join(dir, "sessions.json"))
	if err != nil {
		log.Printf("[session] init failed: %v", err)
		return
	}
	chats, err := memory.NewChatStore(filepath.Join(dir, "chat_sessions.json"))
	if err != nil {
		log.Printf("[chat] store init failed: %v", err)
		return
	}
	sessionRuntimeOnce.sessions = store
	sessionRuntimeOnce.chats = chats
}

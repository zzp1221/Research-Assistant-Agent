package api

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/apppaths"
	"research-assistant-backend/internal/memory"

	"github.com/gin-gonic/gin"
)

var memoryRuntimeOnce struct {
	sync.Once
	store    memory.Store
	settings *memory.SettingsStore
	enabled  bool
}

// MemoryStore returns the process-wide memory store (nil when disabled).
func MemoryStore() memory.Store {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.store
}

// MemorySettings returns the process-wide settings store.
func MemorySettings() *memory.SettingsStore {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.settings
}

// MemoryEnabled reports whether memory injection is enabled. MEMORY_ENABLED=false
// is the documented rollback switch; shadow mode still retrieves but callers
// must not inject.
func MemoryEnabled() bool {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.enabled
}

// CloseRuntimeStores flushes and closes the process-wide memory and wiki
// stores. It is called from the graceful shutdown path so file locks are
// released and snapshots are written before the process exits.
func CloseRuntimeStores() {
	if memoryRuntimeOnce.store != nil {
		_ = memoryRuntimeOnce.store.Close()
	}
	if wikiRuntimeOnce.store != nil {
		_ = wikiRuntimeOnce.store.Close()
	}
}

func MemoryMode() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MEMORY_MODE")))
	if raw == "" {
		return "on"
	}
	return raw
}

// MemoryDirectory resolves the on-disk memory root once per process.
func MemoryDirectory() string {
	dir := strings.TrimSpace(os.Getenv("MEMORY_DIR"))
	if dir == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			dir = filepath.Join(filepath.Dir(planPath), "memory")
		} else {
			dir = filepath.Join(apppaths.DataDir(), "memory")
		}
	}
	return dir
}

func initMemoryRuntime() {
	rawEnabled := strings.TrimSpace(os.Getenv("MEMORY_ENABLED"))
	enabled := true
	if rawEnabled != "" && (strings.EqualFold(rawEnabled, "false") || rawEnabled == "0" || strings.EqualFold(rawEnabled, "off")) {
		enabled = false
	}
	memoryRuntimeOnce.enabled = enabled
	if !enabled {
		return
	}
	dir := MemoryDirectory()
	store, err := memory.NewFileStore(dir)
	if err != nil {
		log.Printf("[memory] init failed dir=%s: %v (falling back to in-memory store)", dir, err)
		store = memory.NewMemoryStore()
	}
	memoryRuntimeOnce.store = store
	settings, settingsErr := memory.NewSettingsStore(filepath.Join(dir, "settings.json"))
	if settingsErr != nil {
		log.Printf("[memory] settings init failed: %v", settingsErr)
	} else {
		memoryRuntimeOnce.settings = settings
	}
	log.Printf("[memory] store ready dir=%s mode=%s", dir, MemoryMode())
	if raw := strings.TrimSpace(os.Getenv("MEMORY_CONSOLIDATE_INTERVAL_SECONDS")); raw != "" {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 {
			memory.NewConsolidator(store).Start(context.Background(), time.Duration(seconds)*time.Second)
		}
	}
}

func registerMemoryRuntime(apiGroup *gin.RouterGroup) {
	store := MemoryStore()
	if store == nil {
		return
	}
	RegisterMemoryRoutes(apiGroup, store, MemorySettings())
}

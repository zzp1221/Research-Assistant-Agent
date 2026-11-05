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
	store     memory.Store
	settings  *memory.SettingsStore
	enabled   bool
	degraded  bool
	ephemeral bool
	reason    string
}

// MemoryStore returns the process-wide memory store (nil when disabled or when
// initialization failed closed).
func MemoryStore() memory.Store {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.store
}

// MemorySettings returns the process-wide settings store.
func MemorySettings() *memory.SettingsStore {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.settings
}

// MemoryEnabled reports whether memory is configured. A degraded store still
// reports enabled=true but MemoryStore() returns nil.
func MemoryEnabled() bool {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.enabled
}

// MemoryAvailable reports whether the store is usable.
func MemoryAvailable() bool {
	memoryRuntimeOnce.Do(initMemoryRuntime)
	return memoryRuntimeOnce.store != nil
}

// MemoryHealth is embedded in /api/health so degraded storage is explicit.
func MemoryHealth() gin.H {
	health := gin.H{
		"ok":        memoryRuntimeOnce.store != nil || !memoryRuntimeOnce.enabled,
		"enabled":   memoryRuntimeOnce.enabled,
		"degraded":  memoryRuntimeOnce.degraded,
		"ephemeral": memoryRuntimeOnce.ephemeral,
	}
	if memoryRuntimeOnce.reason != "" {
		health["reason"] = memoryRuntimeOnce.reason
	}
	return health
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

func allowEphemeralMemory() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("MEMORY_ALLOW_EPHEMERAL")), "true")
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
		memoryRuntimeOnce.degraded = true
		memoryRuntimeOnce.reason = err.Error()
		if strings.Contains(strings.ToLower(err.Error()), "lock") {
			// Another process owns this data directory. Serving a second,
			// divergent view is worse than failing startup loudly.
			log.Printf("[memory] lock conflict dir=%s: %v (exiting)", dir, err)
			os.Exit(1)
		}
		if allowEphemeralMemory() {
			log.Printf("[memory] init failed dir=%s: %v (MEMORY_ALLOW_EPHEMERAL=true, using ephemeral store)", dir, err)
			store = memory.NewMemoryStore()
			memoryRuntimeOnce.store = store
			memoryRuntimeOnce.ephemeral = true
		} else {
			log.Printf("[memory] init failed dir=%s: %v (fail-closed; memory routes will return 503)", dir, err)
			return
		}
	} else {
		memoryRuntimeOnce.store = store
	}
	settings, settingsErr := memory.NewSettingsStore(filepath.Join(dir, "settings.json"))
	if settingsErr != nil {
		log.Printf("[memory] settings init failed: %v", settingsErr)
	} else {
		memoryRuntimeOnce.settings = settings
	}
	log.Printf("[memory] store ready dir=%s mode=%s degraded=%v", dir, MemoryMode(), memoryRuntimeOnce.degraded)
	if raw := strings.TrimSpace(os.Getenv("MEMORY_CONSOLIDATE_INTERVAL_SECONDS")); raw != "" {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 {
			memory.NewConsolidator(store, settings).Start(context.Background(), time.Duration(seconds)*time.Second)
		}
	}
}

func memoryUnavailable(c *gin.Context) {
	c.JSON(503, gin.H{"error": "memory_store_unavailable", "reason": memoryRuntimeOnce.reason})
}

func registerMemoryRuntime(apiGroup *gin.RouterGroup) {
	if !MemoryEnabled() {
		return
	}
	store := MemoryStore()
	if store == nil {
		apiGroup.Any("/memories", memoryUnavailable)
		apiGroup.Any("/memories/*path", memoryUnavailable)
		return
	}
	RegisterMemoryRoutes(apiGroup, store, MemorySettings())
}

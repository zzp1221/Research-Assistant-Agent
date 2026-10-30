package api

import (
	"log"
	"os"
	"strings"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/tools"
)

// toolAgentRuntime bundles the tool registry and agent profile runtime that
// the plan runtime, scheduler and settings APIs share.
type toolAgentRuntime struct {
	tools         *tools.Registry
	invoker       *tools.Invoker
	profiles      *agentprofile.Registry
	profileRunner *agentprofile.ProfileRunner
}

func buildToolAgentRuntime(planStorePath string) *toolAgentRuntime {
	toolPath := strings.TrimSpace(os.Getenv("TOOL_STORE_PATH"))
	if toolPath == "" && planStorePath != "" {
		toolPath = planStorePath + ".tools.json"
	}
	var toolStore tools.Store = tools.NewMemoryStore()
	if toolPath != "" {
		if fileStore, err := tools.NewFileStore(toolPath); err != nil {
			log.Printf("tool store init failed, falling back to memory: %v", err)
		} else {
			toolStore = fileStore
		}
	}
	toolRegistry := tools.NewRegistry(toolStore)
	if err := toolRegistry.EnsureBuiltins(); err != nil {
		log.Printf("tool builtin seed failed: %v", err)
	}
	invoker := tools.NewInvoker(toolRegistry)

	agentPath := strings.TrimSpace(os.Getenv("AGENT_PROFILE_STORE_PATH"))
	if agentPath == "" && planStorePath != "" {
		agentPath = planStorePath + ".agents.json"
	}
	var agentStore agentprofile.Store = agentprofile.NewMemoryStore()
	if agentPath != "" {
		if fileStore, err := agentprofile.NewFileStore(agentPath); err != nil {
			log.Printf("agent profile store init failed, falling back to memory: %v", err)
		} else {
			agentStore = fileStore
		}
	}
	profileRegistry := agentprofile.NewRegistry(agentStore)
	if err := profileRegistry.EnsureBuiltins(); err != nil {
		log.Printf("agent profile builtin seed failed: %v", err)
	}
	var completer agentprofile.Completer
	if resolver := modelresolver.Default(); resolver != nil {
		completer = resolver
	}
	profileRunner := &agentprofile.ProfileRunner{Profiles: profileRegistry, Completer: completer}
	return &toolAgentRuntime{tools: toolRegistry, invoker: invoker, profiles: profileRegistry, profileRunner: profileRunner}
}

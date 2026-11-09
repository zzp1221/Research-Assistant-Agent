package agentprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
)

// RunStore holds private source messages and committed side-effect boundaries.
// One lock covers the whole run; file commits are atomic and precede dispatch.
type RunStore struct {
	Dir   string
	locks *contextwindow.Store
}

func NewRunStore(dir string) *RunStore {
	return &RunStore{Dir: dir, locks: contextwindow.NewStore(filepath.Join(dir, "locks"))}
}

type runCheckpoint struct {
	Run       models.AgentRunState             `json:"run"`
	Binding   string                           `json:"binding"`
	Profile   models.AgentProfile              `json:"profile"`
	Messages  []oaichat.Message                `json:"messages"`
	Pending   []oaichat.ToolCall               `json:"pending"`
	Cursor    int                              `json:"cursor"`
	InFlight  string                           `json:"in_flight,omitempty"`
	Calls     []models.ToolCallRecord          `json:"calls"`
	Artifacts []models.Artifact                `json:"artifacts"`
	Output    string                           `json:"output,omitempty"`
	Tools     map[string]models.ToolDefinition `json:"tools"`
}

func (s *RunStore) acquire(ctx context.Context, key string) (func(), error) {
	if s == nil || s.Dir == "" {
		return nil, fmt.Errorf("agent_checkpoint_store_unavailable")
	}
	return s.locks.Acquire(ctx, key)
}
func (s *RunStore) path(key string) string {
	return filepath.Join(s.Dir, contextwindow.Hash([]byte(key))+".json")
}
func (s *RunStore) load(key string) (*runCheckpoint, error) {
	raw, err := os.ReadFile(s.path(key))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved runCheckpoint
	if json.Unmarshal(raw, &saved) != nil || saved.Run.Version != "agent.run/v1" {
		return nil, fmt.Errorf("agent_checkpoint_invalid")
	}
	return &saved, nil
}
func (s *RunStore) save(key string, saved *runCheckpoint) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.Dir, ".checkpoint-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, s.path(key))
}

type executionGuardKey struct{}

func WithExecutionGuard(ctx context.Context, guard func() error) context.Context {
	return context.WithValue(ctx, executionGuardKey{}, guard)
}
func CheckExecution(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard, ok := ctx.Value(executionGuardKey{}).(func() error); ok {
		return guard()
	}
	return nil
}

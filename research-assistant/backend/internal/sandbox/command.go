package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// Command is the provider-neutral command contract. Argv is the preferred
// form; Shell should only be used for scripts that genuinely need shell
// semantics. Cwd uses the logical /workspace root and is translated by the
// provider.
type Command struct {
	Argv        []string
	Cwd         string
	Env         map[string]string
	Shell       bool
	Timeout     time.Duration
	Stdin       string
	MaxOutputKB int
}

// LegacyShell adapts an old POSIX shell string. Providers that cannot execute
// POSIX shell return unsupported_posix_shell instead of silently succeeding.
func LegacyShell(script string) Command {
	return Command{Argv: []string{"sh", "-lc", script}, Shell: true}
}

type commandRequest struct {
	Argv        []string          `json:"argv,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Shell       bool              `json:"shell,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	Stdin       string            `json:"stdin,omitempty"`
	MaxOutputKB int               `json:"max_output_kb,omitempty"`
}

// EnvList returns sorted KEY=VALUE pairs.
func (c Command) EnvList() []string {
	keys := make([]string, 0, len(c.Env))
	for key := range c.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+c.Env[key])
	}
	return out
}

func (c Command) request() commandRequest {
	timeoutSec := 0
	if c.Timeout > 0 {
		timeoutSec = int(c.Timeout / time.Second)
		if timeoutSec <= 0 {
			timeoutSec = 1
		}
	}
	return commandRequest{
		Argv:        append([]string(nil), c.Argv...),
		Cwd:         c.Cwd,
		Env:         c.Env,
		Shell:       c.Shell,
		TimeoutSec:  timeoutSec,
		Stdin:       c.Stdin,
		MaxOutputKB: c.MaxOutputKB,
	}
}

// ExecCommandV2 executes a Command.
func (s *SandboxClient) ExecCommandV2(ctx context.Context, sandboxID string, command Command) (*PythonRunResponse, error) {
	payload, _ := json.Marshal(command.request())
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+"/commands", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("执行命令失败 (Status %d): %s", resp.StatusCode, string(body))
	}
	var res PythonRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ExecCommandStreamV2 executes a Command and streams its output.
func (s *SandboxClient) ExecCommandStreamV2(ctx context.Context, sandboxID string, command Command, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	return s.streamExecution(ctx, sandboxID, "/commands/stream", command.request(), onChunk)
}

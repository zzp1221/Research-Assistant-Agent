package engine

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Command is the provider-neutral execution contract. Logical paths such as
// /workspace and logical interpreters such as python are translated by each
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

// Normalized returns a copy with empty argv entries removed and the cwd
// trimmed.
func (c Command) Normalized() Command {
	out := Command{Cwd: strings.TrimSpace(c.Cwd), Shell: c.Shell, Timeout: c.Timeout, Stdin: c.Stdin, MaxOutputKB: c.MaxOutputKB}
	for _, arg := range c.Argv {
		if arg != "" {
			out.Argv = append(out.Argv, arg)
		}
	}
	if len(c.Env) > 0 {
		out.Env = make(map[string]string, len(c.Env))
		for key, value := range c.Env {
			out.Env[key] = value
		}
	}
	return out
}

// EnvList returns sorted KEY=VALUE pairs so command lines and tests are
// deterministic.
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

// WithTimeout returns a context honoring the command timeout in addition to the
// caller context.
func (c Command) WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if c.Timeout > 0 {
		return context.WithTimeout(parent, c.Timeout)
	}
	return context.WithCancel(parent)
}

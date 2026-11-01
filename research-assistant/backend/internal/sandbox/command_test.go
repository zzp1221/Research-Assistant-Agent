package sandbox

import (
	"reflect"
	"testing"
	"time"
)

func TestCommandRequestCarriesContract(t *testing.T) {
	command := Command{
		Argv:        []string{"python", "main.py"},
		Cwd:         "/workspace",
		Env:         map[string]string{"PYTHONPATH": "/workspace"},
		Timeout:     90 * time.Second,
		Stdin:       "input",
		MaxOutputKB: 64,
	}
	req := command.request()
	if !reflect.DeepEqual(req.Argv, command.Argv) || req.Cwd != "/workspace" || req.TimeoutSec != 90 || req.MaxOutputKB != 64 {
		t.Fatalf("request = %#v", req)
	}
	if req.Env["PYTHONPATH"] != "/workspace" || req.Stdin != "input" {
		t.Fatalf("env/stdin not carried: %#v", req)
	}
}

func TestLegacyShellUsesShellFlag(t *testing.T) {
	cmd := LegacyShell("cd /workspace && python3 x.py")
	if !cmd.Shell || len(cmd.Argv) != 3 || cmd.Argv[0] != "sh" || cmd.Argv[1] != "-lc" {
		t.Fatalf("LegacyShell = %#v", cmd)
	}
}

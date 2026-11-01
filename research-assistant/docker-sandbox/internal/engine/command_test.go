package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDockerExecArgsTranslatesCommand(t *testing.T) {
	args := dockerExecArgs("abc123", Command{
		Argv: []string{"python", "script.py"},
		Cwd:  "/workspace",
		Env:  map[string]string{"B": "2", "A": "1"},
	})
	want := []string{
		"exec", "-w", "/workspace",
		"-e", "A=1", "-e", "B=2",
		"-e", "RA_OUTPUT_DIR=/workspace/output",
		"abc123", "python3", "script.py",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("dockerExecArgs = %#v, want %#v", args, want)
	}
}

func TestDockerExecArgsWrapsShell(t *testing.T) {
	args := dockerExecArgs("abc123", Command{Argv: []string{"python", "x.py"}, Shell: true})
	if got := args[len(args)-3:]; !reflect.DeepEqual(got, []string{"sh", "-lc", "python3 x.py"}) {
		t.Fatalf("shell tail = %#v", got)
	}
}

func TestDockerExecArgsDoesNotDoubleWrapLegacyShell(t *testing.T) {
	args := dockerExecArgs("abc123", Command{Argv: []string{"sh", "-lc", "echo hi"}, Shell: true})
	tail := args[len(args)-3:]
	if !reflect.DeepEqual(tail, []string{"sh", "-lc", "echo hi"}) {
		t.Fatalf("shell tail = %#v", tail)
	}
}

func TestApplyOutputLimit(t *testing.T) {
	res := &ExecutionResponse{Stdout: strings.Repeat("a", 2048), Stderr: strings.Repeat("b", 2048)}
	applyOutputLimit(res, 1)
	if !res.Truncated {
		t.Fatalf("Truncated = false")
	}
	if len(res.Stdout) > 1024+len("...[truncated]") {
		t.Fatalf("stdout too long: %d", len(res.Stdout))
	}
	if len(res.Stderr) > 1024+len("...[truncated]") {
		t.Fatalf("stderr too long: %d", len(res.Stderr))
	}
}

func TestCommandNormalizedAndEnvList(t *testing.T) {
	cmd := Command{Argv: []string{"python", "", "x.py"}, Cwd: "  /workspace ", Env: map[string]string{"B": "2", "A": "1"}, Timeout: 3 * time.Second}
	normalized := cmd.Normalized()
	if !reflect.DeepEqual(normalized.Argv, []string{"python", "x.py"}) {
		t.Fatalf("argv = %#v", normalized.Argv)
	}
	if normalized.Cwd != "/workspace" {
		t.Fatalf("cwd = %q", normalized.Cwd)
	}
	if got := normalized.EnvList(); !reflect.DeepEqual(got, []string{"A=1", "B=2"}) {
		t.Fatalf("env = %#v", got)
	}
}

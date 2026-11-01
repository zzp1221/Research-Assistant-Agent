package vm

import (
	"reflect"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestToWSLPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\Users\me\ws`, "/mnt/c/Users/me/ws"},
		{"D:/data/project", "/mnt/d/data/project"},
		{"/already/linux", "/already/linux"},
	}
	for _, item := range cases {
		got, err := ToWSLPath(item.in)
		if err != nil || got != item.want {
			t.Fatalf("ToWSLPath(%q) = %q, %v; want %q", item.in, got, err, item.want)
		}
	}
	if _, err := ToWSLPath("relative/path"); err == nil {
		t.Fatal("relative path should fail")
	}
	if _, err := ToWSLPath(`\\server\share`); err == nil {
		t.Fatal("UNC path should fail")
	}
}

func TestFromWSLPath(t *testing.T) {
	got, err := FromWSLPath("/mnt/c/Users/me/ws")
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\Users\me\ws` {
		t.Fatalf("FromWSLPath = %q", got)
	}
	if _, err := FromWSLPath("/home/me"); err == nil {
		t.Fatal("non-/mnt path should fail")
	}
}

func TestBuildWSLArgs(t *testing.T) {
	got := BuildWSLArgs("Ubuntu-22.04", "/mnt/c/ws", []string{"python3", "--version"})
	want := []string{"-d", "Ubuntu-22.04", "--cd", "/mnt/c/ws", "--", "python3", "--version"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	empty := BuildWSLArgs("", "", []string{"true"})
	if !reflect.DeepEqual(empty, []string{"--", "true"}) {
		t.Fatalf("empty args = %#v", empty)
	}
}

func TestBuildWSLArgsWithEnv(t *testing.T) {
	got := BuildWSLArgsWithEnv("Ubuntu", "/ws", map[string]string{"B": "2", "A": "1"}, []string{"env"})
	want := []string{"-d", "Ubuntu", "--cd", "/ws", "--", "env", "A=1", "B=2", "env"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestTranslateVMCommand(t *testing.T) {
	argv, cwd, err := TranslateVMCommand(`C:\workspace`, engine.Command{Argv: []string{"python", "-c", "print(1)"}, Cwd: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(argv, []string{"python3", "-c", "print(1)"}) {
		t.Fatalf("argv = %#v", argv)
	}
	if cwd != "/mnt/c/workspace" {
		t.Fatalf("cwd = %q", cwd)
	}
	_, subdir, err := TranslateVMCommand(`C:\workspace`, engine.Command{Argv: []string{"ls"}, Cwd: "/workspace/data"})
	if err != nil || subdir != "/mnt/c/workspace/data" {
		t.Fatalf("subdir = %q, %v", subdir, err)
	}
}

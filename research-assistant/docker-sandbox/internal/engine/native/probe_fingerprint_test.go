package native

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Reproduce the previous serialization rather than assuming SHA-256 itself
// distinguishes ambiguous streams. All fixture data is synthetic.
func oldUnframedProbeFiles(t *testing.T, root string) string {
	t.Helper()
	root, err := canonicalEnvironmentPath(root)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			paths = append(paths, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		fmt.Fprintln(h, path)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestProbeFingerprintSeparatesPreviouslyCollidingFileSets(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python.exe")
	padding := filepath.Join(root, "a-padding.bin")
	startup := filepath.Join(root, "b-startup.py")
	if err := os.WriteFile(python, []byte("synthetic-interpreter"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalEnvironmentPath(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("print('synthetic-startup')\n")
	if err := os.WriteFile(padding, append([]byte(filepath.Join(canonicalRoot, "b-startup.py")+"\n"), payload...), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeOld := oldUnframedProbeFiles(t, root)
	before, err := ProbeEnvironmentKey(python, "environment", "configuration")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(padding, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(startup, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if afterOld := oldUnframedProbeFiles(t, root); beforeOld != afterOld {
		t.Fatal("fixture did not reproduce the old ambiguous file stream")
	}
	after, err := ProbeEnvironmentKey(python, "environment", "configuration")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("different executable file sets reused the same framed fingerprint")
	}
}

func TestProbeFingerprintFramesMetadataAndHashesBytecode(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python.exe")
	if err := os.WriteFile(python, []byte("synthetic-interpreter"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ProbeEnvironmentKey(python, "environment with", "spaces")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProbeEnvironmentKey(python, "environment", "with spaces")
	if err != nil || first == second {
		t.Fatal("metadata fields were not independently framed")
	}
	cache := filepath.Join(root, "__pycache__")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "stdlib.cpython-312.pyc"), []byte("synthetic-bytecode"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := ProbeEnvironmentKey(python, "environment", "with spaces")
	if err != nil || second == third {
		t.Fatal("loadable bytecode was omitted from the environment fingerprint")
	}
}

func TestProbeNetworkEndpointParsing(t *testing.T) {
	for _, tc := range []struct {
		endpoint, host string
		port           int
	}{
		{"127.0.0.1:2345", "127.0.0.1", 2345},
		{"controlled.example:443", "controlled.example", 443},
		{"[::1]:2345", "::1", 2345},
		{"[fe80::1%3]:443", "fe80::1%3", 443},
	} {
		host, port, err := splitProbeEndpoint(tc.endpoint)
		if err != nil || host != tc.host || port != tc.port {
			t.Fatalf("endpoint %q: got %q:%d (%v)", tc.endpoint, host, port, err)
		}
	}
	for _, endpoint := range []string{"", ":443", "::1:2345", "host:0", "host:-1", "host:65536", "host:https"} {
		if _, _, err := splitProbeEndpoint(endpoint); err == nil {
			t.Fatalf("invalid endpoint %q was accepted", endpoint)
		}
	}
}

package native

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test executable doubles as a real cross-platform uv/build-hook fixture.
// It never needs a parent-environment marker to enter fixture mode.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "native-env-check":
			os.Exit(checkChildEnvironment())
		case "native-env-sleep":
			time.Sleep(time.Hour)
			os.Exit(0)
		case "native-workspace-write-check":
			if len(os.Args) != 3 {
				os.Exit(96)
			}
			err := os.WriteFile(os.Args[2], []byte("low-workspace-output"), 0o600)
			if os.IsPermission(err) {
				fmt.Print("existing-host-object-write-denied")
				os.Exit(95)
			}
			if err != nil {
				os.Exit(96)
			}
			fmt.Print("new-workspace-output-written")
			os.Exit(0)
		case "python", "venv", "pip":
			os.Exit(fakeUV())
		}
	}
	os.Exit(m.Run())
}

func checkChildEnvironment() int {
	for _, name := range []string{"SANDBOX_API_TOKEN", "API_AUTH_TOKEN", "IDENTITY_COOKIE_SECRET", "NATIVE_UNKNOWN_SENTINEL", "OPENAI_API_KEY", "HTTP_PROXY", "PYTHONSTARTUP"} {
		if _, found := os.LookupEnv(name); found {
			return 81
		}
	}
	if os.Getenv("HOME") == "" || os.Getenv("TMP") == "" {
		return 82
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("TMP"), "env-ok.txt"), []byte("ok"), 0o600); err != nil {
		if e, ok := err.(*os.PathError); ok {
			fmt.Printf("private-temp-write-error=%v", e.Err)
		}
		return 83
	}
	fmt.Print("safe-environment")
	return 0
}

func fakeUV() int {
	if os.Getenv("UV_LINK_MODE") != "copy" {
		return 97
	}
	// runUV fixes cwd to the installation's own build workspace.
	root, err := os.Getwd()
	if err != nil {
		return 90
	}
	log, err := os.OpenFile(filepath.Join(root, "uv.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 91
	}
	fmt.Fprintln(log, strings.Join(os.Args[1:], " "))
	_ = log.Close()
	switch os.Args[1] {
	case "python":
		return 0
	case "venv":
		if len(os.Args) < 3 {
			return 92
		}
		python := pythonExecutable(os.Args[2])
		if os.MkdirAll(filepath.Dir(python), 0o700) != nil || os.WriteFile(python, []byte("fixture"), 0o700) != nil {
			return 93
		}
	case "pip":
		if len(os.Args) > 2 && os.Args[2] == "freeze" {
			fmt.Print("numpy==1.0\nsix==1.0\n")
		}
		// Stand in for an untrusted build backend spawned by uv. It receives
		// uv's actual environment, without another sanitizer in the fixture.
		if len(os.Args) > 2 && os.Args[2] == "install" {
			self, _ := os.Executable()
			child := exec.Command(self, "native-env-check")
			if child.Run() != nil {
				return 94
			}
		}
	}
	return 0
}

func fixtureUV(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

func syntheticSecrets(t *testing.T) {
	t.Helper()
	for _, name := range []string{"SANDBOX_API_TOKEN", "API_AUTH_TOKEN", "IDENTITY_COOKIE_SECRET", "NATIVE_UNKNOWN_SENTINEL", "OPENAI_API_KEY", "HTTP_PROXY", "PYTHONSTARTUP"} {
		t.Setenv(name, "synthetic-secret-only")
	}
}

func environmentMap(entries []string) map[string]string {
	result := map[string]string{}
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			result[strings.ToUpper(parts[0])] = parts[1]
		}
	}
	return result
}

func TestResearchEnvironmentDropsParentSecretsAndUnknownVariables(t *testing.T) {
	syntheticSecrets(t)
	root := t.TempDir()
	interpreter := filepath.Join(root, "venv", "Scripts", "python.exe")
	env, err := researchEnvironment(root, interpreter, map[string]string{"OMP_NUM_THREADS": "2", "seed": "42", "RA_TASK_DATASET": "中文数据"})
	if err != nil {
		t.Fatal(err)
	}
	actual := environmentMap(env)
	for _, name := range []string{"SANDBOX_API_TOKEN", "API_AUTH_TOKEN", "IDENTITY_COOKIE_SECRET", "NATIVE_UNKNOWN_SENTINEL", "OPENAI_API_KEY", "HTTP_PROXY", "PYTHONSTARTUP"} {
		if _, found := actual[name]; found {
			t.Fatalf("parent variable %s entered the research environment", name)
		}
	}
	if actual["OMP_NUM_THREADS"] != "2" || actual["SEED"] != "42" || actual["RA_TASK_DATASET"] != "中文数据" {
		t.Fatal("explicit research parameters were not preserved")
	}
	for _, name := range []string{"HOME", "TEMP", "TMP", "MPLCONFIGDIR", "HF_HOME", "PIP_CACHE_DIR", "APPDATA"} {
		if !strings.HasPrefix(actual[name], filepath.Join(root, ".ra-env")) {
			t.Fatalf("%s is not private to the workspace", name)
		}
	}
	if strings.Contains(actual["PATH"], "synthetic-secret") {
		t.Fatal("parent search path was copied")
	}
	// Each call owns a new snapshot.
	actual["SEED"] = "999"
	again, err := researchEnvironment(root, interpreter, map[string]string{"SEED": "42"})
	if err != nil || environmentMap(again)["SEED"] != "42" {
		t.Fatal("environment snapshots share mutable state")
	}
}

func TestTaskEnvironmentRejectsProtectedAndAmbiguousOverrides(t *testing.T) {
	cases := []map[string]string{
		{"sandbox_api_token": "x"}, {"Api_Auth_Token": "x"}, {"IDENTITY_COOKIE_SECRET": "x"},
		{"PATH": "x"}, {"PYTHONHOME": "x"}, {"PYTHONSTARTUP": "x"}, {"PYTHONINSPECT": "1"},
		{"HTTP_PROXY": "x"}, {"UV_INDEX_URL": "x"}, {"APPDATA": "x"}, {"UNKNOWN": "x"},
		{"SEED": "1", "seed": "2"}, {"RA_TASK_BAD=NAME": "x"}, {"RA_TASK_OK": "x\x00y"},
		{"OMP_NUM_THREADS": "-1"}, {"PYTHONHASHSEED": "random"}, {"SEED": "4294967296"},
		{"RA_TASK_LONG": strings.Repeat("x", 8193)}, {"": "x"},
	}
	for i, env := range cases {
		if _, err := validateTaskEnvironment(env); err == nil {
			t.Errorf("case %d accepted forbidden task environment", i)
		}
	}
	large := map[string]string{}
	for _, key := range []string{"RA_TASK_ONE", "RA_TASK_TWO", "RA_TASK_THREE", "RA_TASK_FOUR", "RA_TASK_FIVE"} {
		large[key] = strings.Repeat("x", 7000)
	}
	if _, err := validateTaskEnvironment(large); err == nil {
		t.Fatal("aggregate environment limit was not enforced")
	}
}

func TestTranslatePythonPathRestrictsWorkspaceAndTraversal(t *testing.T) {
	root := t.TempDir()
	paths, err := translatePythonPath(root, "/workspace/src:/workspace")
	if err != nil || paths != filepath.Join(root, "src")+string(os.PathListSeparator)+root {
		t.Fatalf("safe Python path translation failed: %v", err)
	}
	for _, path := range []string{"", "/tmp", "/workspace/../escape", "/workspace/./src", "/workspace\\escape", "/workspace;C:\\secret", "C:/workspace", "/workspace:/outside", "/workspace/:/workspace/../../outside"} {
		if _, err := translatePythonPath(root, path); err == nil {
			t.Errorf("accepted unsafe logical Python path %q", path)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(root, "external")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := translatePythonPath(root, "/workspace/external/nested"); err == nil {
			t.Fatal("Python path followed a symlink outside the workspace")
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	} else {
		t.Log("symlink creation is unavailable; Windows junction coverage is in the native integration suite")
	}
}

func TestResearchEnvironmentValidatesAgainBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	if _, err := researchEnvironment(root, "", map[string]string{"PYTHONPATH": t.TempDir()}); err == nil {
		t.Fatal("launch validation accepted an outside Python path")
	}
	if _, err := researchEnvironment(root, "", map[string]string{"PYTHONPATH": "relative"}); err == nil {
		t.Fatal("launch validation accepted a relative Python path")
	}
	if _, err := researchEnvironment(root, "", map[string]string{"PATH": root}); err == nil {
		t.Fatal("launch validation accepted a PATH override")
	}
}

func TestRuntimeBuildHookReceivesSanitizedEnvironment(t *testing.T) {
	syntheticSecrets(t)
	root := t.TempDir()
	manager := NewRuntimeManager(fixtureUV(t), filepath.Join(root, "runtime"), filepath.Join(root, "venvs"), filepath.Join(root, "logs"))
	_, err := manager.EnsureRuntime(t.Context(), "3.11", []string{"fixture-build-backend"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "runtime", ".ra-build", "uv.log"))
	if err != nil || !strings.Contains(string(data), "pip install") {
		t.Fatal("fixture did not execute the third-party build-hook path")
	}
}

func TestInstallationEnvironmentDoesNotInheritUserConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("UV_INDEX_URL", "synthetic-index")
	t.Setenv("PIP_CONFIG_FILE", "synthetic-config")
	t.Setenv("HOME", "synthetic-home")
	env, err := installationEnvironment(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	actual := environmentMap(env)
	if actual["UV_INDEX_URL"] != "" || actual["PIP_CONFIG_FILE"] != os.DevNull || actual["UV_NO_CONFIG"] != "1" || actual["UV_LINK_MODE"] != "copy" {
		t.Fatal("installer imported user configuration")
	}
	if !strings.HasPrefix(actual["HOME"], root) {
		t.Fatal("installer imported the user's home directory")
	}
}

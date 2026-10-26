package agent

import (
	"testing"
)

func TestGeneratedStandardLibraryListCoversCommonModules(t *testing.T) {
	for _, module := range []string{"bisect", "heapq", "array", "ast", "enum", "decimal", "sqlite3", "ssl", "http", "email", "xml", "multiprocessing", "concurrent", "shutil", "pathlib"} {
		if !isPythonStandardLibraryModule(module) {
			t.Fatalf("%q should be recognized as a Python standard-library module", module)
		}
	}
	if isPythonStandardLibraryModule("numpy") {
		t.Fatal("numpy must not be treated as a standard-library module")
	}
}

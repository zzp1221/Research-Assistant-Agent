package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"research-assistant-backend/internal/llmclient"
	"strings"

	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

func pipErrorIndicatesPythonVersionMismatch(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "requires-python") && (strings.Contains(lower, ">=3.10") || strings.Contains(lower, ">=3.11") || strings.Contains(lower, ">=3.12")) {
		return true
	}
	if strings.Contains(lower, "require a different python version") {
		return true
	}
	return false
}

func choosePythonUpgradeImage(text string) string {
	lower := strings.ToLower(text)
	// 优先满足更高版本要求；默认 3.11（兼容运行 3.9 代码，且能覆盖多数 Requires-Python>=3.10/3.11 的包）
	if strings.Contains(lower, ">=3.12") {
		return "python:3.12-bullseye"
	}
	if strings.Contains(lower, ">=3.11") {
		return "python:3.11-bullseye"
	}
	if strings.Contains(lower, ">=3.10") {
		return "python:3.10-bullseye"
	}
	return "python:3.11-bullseye"
}

func pipErrorMissingRequirement(text string) (string, bool) {
	// 例：
	// - "ERROR: No matching distribution found for shutil"
	// - "ERROR: Could not find a version that satisfies the requirement X (from versions: ...)"
	for _, prefix := range []string{
		"no matching distribution found for ",
		"could not find a version that satisfies the requirement ",
	} {
		lower := strings.ToLower(text)
		idx := strings.Index(lower, prefix)
		if idx < 0 {
			continue
		}
		raw := strings.TrimSpace(text[idx+len(prefix):])
		if raw == "" {
			continue
		}
		// 截断到空格/括号前，拿到包名 root
		raw = strings.TrimPrefix(raw, ":")
		raw = strings.TrimSpace(raw)
		for _, sep := range []string{" ", "(", ";", ","} {
			if cut := strings.Index(raw, sep); cut >= 0 {
				raw = raw[:cut]
			}
		}
		raw = strings.TrimSpace(strings.Trim(raw, `"'`))
		if raw == "" {
			continue
		}
		root := strings.Split(raw, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		return root, true
	}
	return "", false
}

func dropDependencyByRoot(items []string, root string) []string {
	root = strings.ToLower(strings.TrimSpace(root))
	if root == "" {
		return items
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		normalizedRoot := strings.Split(name, "[")[0]
		normalizedRoot = strings.Split(normalizedRoot, "<")[0]
		normalizedRoot = strings.Split(normalizedRoot, ">")[0]
		normalizedRoot = strings.Split(normalizedRoot, "=")[0]
		normalizedRoot = strings.TrimSpace(normalizedRoot)
		if normalizedRoot == root {
			continue
		}
		out = append(out, item)
	}
	return out
}

func replaceDependencyByRoot(items []string, from string, to string) []string {
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return items
	}
	out := make([]string, 0, len(items))
	replaced := false
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		root := strings.Split(name, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		if root == from {
			out = append(out, to)
			replaced = true
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, to)
	}
	return uniqueDependencies(out)
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func cleanJSONFence(raw string) string {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// stdlibModulesData is generated from python:3.11-bullseye with
//
//	python3 -c "import sys; print('\n'.join(sorted(sys.stdlib_module_names)))"
//
// Keeping it as a data file prevents the hand-maintained 37-module list from
// drifting from the sandbox interpreter again (D4).
//
//go:embed stdlib_modules.txt
var stdlibModulesData string

var pythonStandardLibraryModules = loadStandardLibraryModules()

func loadStandardLibraryModules() map[string]struct{} {
	out := make(map[string]struct{}, 320)
	for _, line := range strings.Split(stdlibModulesData, "\n") {
		name := strings.ToLower(strings.TrimSpace(line))
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

var pythonImportPackageMap = map[string]string{
	"cv2":                              "opencv-python",
	"sklearn":                          "scikit-learn",
	"PIL":                              "pillow",
	"bs4":                              "beautifulsoup4",
	"yaml":                             "pyyaml",
	"llama_index":                      "llama-index",
	"llama_index.llms.openai":          "llama-index-llms-openai",
	"llama_index.embeddings.openai":    "llama-index-embeddings-openai",
	"llama_index.vector_stores.chroma": "llama-index-vector-stores-chroma",
	"langchain_openai":                 "langchain-openai",
	"langchain_community":              "langchain-community",
	"faiss":                            "faiss-cpu",
}

func isPythonStandardLibraryModule(name string) bool {
	root := strings.TrimSpace(name)
	if root == "" {
		return false
	}
	root = strings.Split(root, ".")[0]
	root = strings.Split(root, "<")[0]
	root = strings.Split(root, ">")[0]
	root = strings.Split(root, "=")[0]
	root = strings.Split(root, "[")[0]
	root = strings.ToLower(strings.TrimSpace(root))
	_, ok := pythonStandardLibraryModules[root]
	return ok
}

func mapPythonImportToPackage(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if mapped, ok := pythonImportPackageMap[root]; ok {
		return mapped
	}
	if mapped, ok := pythonImportPackageMap[strings.ToLower(root)]; ok {
		return mapped
	}
	primary := strings.Split(root, ".")[0]
	if mapped, ok := pythonImportPackageMap[primary]; ok {
		return mapped
	}
	if mapped, ok := pythonImportPackageMap[strings.ToLower(primary)]; ok {
		return mapped
	}
	return primary
}

func extractMissingPythonModule(errText string) string {
	for _, marker := range []string{"No module named '", `No module named "`} {
		start := strings.Index(errText, marker)
		if start < 0 {
			continue
		}
		rest := errText[start+len(marker):]
		end := strings.IndexAny(rest, `'"`)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(rest[:end])
	}

	const plainMarker = "No module named "
	start := strings.Index(errText, plainMarker)
	if start < 0 {
		return ""
	}
	rest := strings.TrimSpace(errText[start+len(plainMarker):])
	for _, token := range strings.Fields(rest) {
		token = strings.TrimSpace(strings.Trim(token, `"'.,:;()[]{}<>`))
		if token != "" {
			return token
		}
	}
	return ""
}

func cleanCodeFence(raw string) string {
	cleaned := strings.TrimSpace(raw)
	for _, prefix := range []string{"```python", "```py", "```"} {
		if strings.HasPrefix(cleaned, prefix) {
			cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, prefix))
		}
	}
	if strings.HasSuffix(cleaned, "```") {
		cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "```"))
	}
	return cleaned
}

func shouldAttemptPythonRuntimeCodeRepair(errText string) bool {
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "importerror: cannot import name") ||
		strings.Contains(lower, "attributeerror: module") ||
		strings.Contains(lower, "llama_index") ||
		strings.Contains(lower, "syntaxerror:") ||
		strings.Contains(lower, "f-string: invalid syntax") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "incorrect api key") ||
		strings.Contains(lower, "authenticationerror") ||
		strings.Contains(lower, "sk-placeholder")
}

func (a *CoderAgent) repairGeneratedPythonCodeForRuntimeError(ctx context.Context, code string, errText string) (string, bool, error) {
	if a == nil || a.ChatModel == nil || strings.TrimSpace(code) == "" || !shouldAttemptPythonRuntimeCodeRepair(errText) {
		return "", false, nil
	}

	intentType, _ := ctx.Value(coderIntentTypeContextKey).(string)
	taskType, _ := ctx.Value(coderTaskTypeContextKey).(string)
	taskName, _ := ctx.Value(coderTaskNameContextKey).(string)
	code = truncateToTokens(code, agentEnvInt("CODER_REPAIR_CODE_MAX_TOKENS", 24000))
	errText = truncateToTokens(errText, agentEnvInt("CODER_REPAIR_ERROR_MAX_TOKENS", 8000))
	prompt := prompts.RuntimeCodeRepairUserPromptForTask(errText, code, intentType, taskType, taskName)
	msg, err := llmclient.GenerateWithUsage(ctx, a.ChatModel, []*schema.Message{
		{Role: schema.System, Content: coderSystemPromptFromContext(ctx, a.SystemPrompt)},
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", false, err
	}

	repaired := cleanCodeFence(msg.Content)
	if strings.TrimSpace(repaired) == "" || strings.TrimSpace(repaired) == strings.TrimSpace(code) {
		return "", false, nil
	}
	return repaired, true, nil
}

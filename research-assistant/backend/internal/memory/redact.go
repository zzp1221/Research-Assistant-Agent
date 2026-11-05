package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{16,}`),
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bghp_[a-zA-Z0-9]{20,}`),
	regexp.MustCompile(`(?i)\bxox[baprs]-[a-zA-Z0-9-]{10,}`),
	regexp.MustCompile(`(?i)-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(password|passwd|secret|api[_-]?key|access[_-]?token)\s*[=:]\s*[^\s]{8,}`),
}

var injectionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"disregard previous",
	"忽略之前",
	"忽略以上",
	"忽略前面的",
	"系统指令",
	"system prompt",
	"developer message",
	"you are now",
	"new instructions",
}

// ValidateRecord enforces the safety rules that must hold before a record is
// persisted: no secrets, no hidden benchmark provenance, no huge payloads.
func ValidateRecord(rec *Record) error {
	if rec == nil {
		return fmt.Errorf("memory record is nil")
	}
	if strings.TrimSpace(rec.Scope.OwnerID) == "" {
		return fmt.Errorf("memory owner_id is required")
	}
	if !rec.Kind.Valid() {
		return fmt.Errorf("invalid memory kind: %s", rec.Kind)
	}
	if !rec.Trust.Valid() {
		rec.Trust = TrustVerified
	}
	if !rec.Status.Valid() {
		rec.Status = StatusActive
	}
	title := strings.TrimSpace(rec.Title)
	content := strings.TrimSpace(rec.Content)
	if title == "" && content == "" {
		return fmt.Errorf("memory title or content is required")
	}
	if utf8.RuneCountInString(title) > 300 {
		return fmt.Errorf("memory title exceeds 300 characters")
	}
	if utf8.RuneCountInString(content) > 8000 {
		return fmt.Errorf("memory content exceeds 8000 characters")
	}
	combined := title + "\n" + content
	for _, pattern := range secretPatterns {
		if pattern.MatchString(combined) {
			return fmt.Errorf("memory contains a secret-like value and was rejected")
		}
	}
	if rec.Evidence.Hidden || IsHiddenSource(rec.Source) {
		return fmt.Errorf("hidden benchmark memory cannot be persisted")
	}
	if IsHiddenText(combined) {
		return fmt.Errorf("memory contains a hidden benchmark marker")
	}
	if hasLargeBase64(combined) {
		return fmt.Errorf("memory contains a large base64 payload and was rejected")
	}
	if matched, pattern := ScanInjection(combined); matched {
		markInjection(rec, pattern)
	}
	for index := range rec.Refs {
		ref := &rec.Refs[index]
		if utf8.RuneCountInString(ref.Quote) > 500 {
			return fmt.Errorf("memory ref %d quote exceeds 500 characters", index)
		}
		if ref.DocumentID != "" && IsHiddenSource(Source{Ref: ref.DocumentID}) {
			return fmt.Errorf("memory ref %d points at a hidden document", index)
		}
		if ref.ArtifactRef != "" && IsHiddenSource(Source{Ref: ref.ArtifactRef}) {
			return fmt.Errorf("memory ref %d points at a hidden artifact", index)
		}
		if IsHiddenText(ref.Quote) {
			return fmt.Errorf("memory ref %d quote contains a hidden benchmark marker", index)
		}
		if matched, _ := ScanInjection(ref.Quote); matched {
			// Never persist an injected quote as trusted content: downgrade so it
			// cannot be injected into a prompt.
			rec.Trust = TrustUntrusted
		}
	}
	if rec.Attributes != nil {
		if err := walkAttributes(rec.Attributes, 1, "attributes", rec); err != nil {
			return err
		}
	}
	return nil
}

// markInjection downgrades a record and records why. Injection is data, not an
// instruction, so the record is kept (never silently dropped).
func markInjection(rec *Record, pattern string) {
	rec.Trust = TrustUntrusted
	if rec.Attributes == nil {
		rec.Attributes = map[string]any{}
	}
	rec.Attributes["injection_flag"] = true
	rec.Attributes["injection_pattern"] = pattern
}

// walkAttributes validates nested attribute values. Depth is capped at 5 so a
// hostile payload cannot cause unbounded recursion.
func walkAttributes(value any, depth int, path string, rec *Record) error {
	if depth > 5 {
		return fmt.Errorf("memory attribute %q exceeds maximum nesting depth", path)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if err := walkAttributes(item, depth+1, path+"."+key, rec); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range typed {
			if err := walkAttributes(item, depth+1, fmt.Sprintf("%s[%d]", path, index), rec); err != nil {
				return err
			}
		}
	case []string:
		for index, item := range typed {
			if err := walkAttributes(item, depth+1, fmt.Sprintf("%s[%d]", path, index), rec); err != nil {
				return err
			}
		}
	case string:
		if IsHiddenText(typed) {
			return fmt.Errorf("memory attribute %q contains a hidden benchmark marker", path)
		}
		if matched, pattern := ScanInjection(typed); matched {
			markInjection(rec, pattern)
		}
	}
	return nil
}

// IsHiddenText reports whether arbitrary text carries a hidden-benchmark
// marker. PaperPage titles/abstracts/authors are scanned with this before they
// become a new persistence surface outside the memory store.
func IsHiddenText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{"hidden benchmark", "benchmark_private", "private-benchmark", "benchmark-private"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	root := strings.TrimSpace(os.Getenv("BENCHMARK_PRIVATE_ROOT"))
	if root != "" && strings.Contains(lower, strings.ToLower(filepath.Clean(root))) {
		return true
	}
	return false
}

// IsHiddenSource rejects records whose source points into the private benchmark
// root. The check is deliberately conservative: path/URL substrings plus the
// hidden benchmark markers are enough to fail closed.
func IsHiddenSource(source Source) bool {
	root := strings.TrimSpace(os.Getenv("BENCHMARK_PRIVATE_ROOT"))
	candidates := []string{source.Ref, source.URL}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		lower := strings.ToLower(candidate)
		if strings.Contains(lower, "hidden") || strings.Contains(lower, "benchmark_private") || strings.Contains(lower, "private-benchmark") {
			return true
		}
		if root != "" {
			cleanRoot := filepath.Clean(root)
			if strings.HasPrefix(filepath.Clean(candidate), cleanRoot) {
				return true
			}
		}
	}
	return false
}

func hasLargeBase64(text string) bool {
	run := 0
	for _, r := range text {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			run++
			if run > 8192 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// ScanInjection reports whether text looks like a prompt-injection attempt.
// The result is used to downgrade trust and to keep the memory block labelled
// as data, never as instructions.
func ScanInjection(text string) (bool, string) {
	lower := strings.ToLower(text)
	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, pattern) {
			return true, pattern
		}
	}
	return false, ""
}

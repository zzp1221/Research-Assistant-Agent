package agent

import (
	"strings"
	"testing"
)

func TestFormatTaskInputsOmitsImagesAndTruncates(t *testing.T) {
	inputs := map[string]any{
		"plot_png":      "iVBORw0KGgo" + strings.Repeat("A", 5000),
		"execution_log": "saved: /workspace/output_plot.png\nself-check passed",
		"long_report":   strings.Repeat("x", 20),
	}
	text := formatTaskInputsForPrompt(inputs, 10)
	if strings.Contains(text, "iVBORw0KGgo") {
		t.Fatalf("image bytes leaked into the prompt: %s", text)
	}
	if !strings.Contains(text, "image/base64 artifact omitted") {
		t.Fatalf("image placeholder missing: %s", text)
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("long value was not truncated: %s", text)
	}
	if !strings.Contains(text, "- execution_log:") || !strings.Contains(text, "- plot_png:") {
		t.Fatalf("keys missing: %s", text)
	}
	if strings.Index(text, "- execution_log:") > strings.Index(text, "- plot_png:") {
		t.Fatalf("keys are not sorted: %s", text)
	}
}

func TestFormatTaskInputsEmpty(t *testing.T) {
	if got := formatTaskInputsForPrompt(nil, 100); got != "" {
		t.Fatalf("empty inputs=%q", got)
	}
}

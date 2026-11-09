package modelprofile

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func EnvironmentContextWindow() int {
	for _, key := range []string{"MODEL_CONTEXT_WINDOW_TOKENS", "PROMPT_CONTEXT_WINDOW"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			n, _ := strconv.Atoi(raw)
			return n
		}
	}
	return 0
}

const (
	APIChat              = "chat_completions"
	APIResponses         = "responses"
	CompactionOff        = "off"
	CompactionStandalone = "standalone"
	CompactionServer     = "server"
	CompactionText       = "text_summary"
)

// Capabilities are administrator assertions about this deployment, independent
// of its OpenAI-compatible provider label. Unknown never enables native APIs.
type Capabilities struct {
	Responses            string `json:"responses"`
	StandaloneCompaction string `json:"standalone_compaction"`
	ServerCompaction     string `json:"server_compaction"`
	RenderedInputCount   string `json:"rendered_input_count"`
	ToolCalling          string `json:"tool_calling"`
}

type ContextPolicy struct {
	APIFormat              string       `json:"api_format"`
	ContextWindowSource    string       `json:"context_window_source"`
	CompactionMode         string       `json:"compaction_mode"`
	CompactionTriggerRatio float64      `json:"compaction_trigger_ratio"`
	TextSummaryTargetRatio float64      `json:"text_summary_target_ratio"`
	Capabilities           Capabilities `json:"capabilities"`
}

// NormalizeContext migrates old profiles without inventing model capacities.
func (p *Profile) NormalizeContext() {
	if p.APIFormat == "" {
		p.APIFormat = APIChat
	}
	if p.CompactionMode == "" {
		p.CompactionMode = CompactionOff
	}
	if p.ContextWindowSource == "" {
		p.ContextWindowSource = "unknown"
		if p.ContextWindowTokens > 0 {
			p.ContextWindowSource = "existing_config"
		}
	}
	if p.CompactionTriggerRatio == 0 {
		p.CompactionTriggerRatio = .8
	}
	if p.TextSummaryTargetRatio == 0 {
		p.TextSummaryTargetRatio = .55
	}
	c := &p.Capabilities
	if c.ToolCalling == "" {
		c.ToolCalling = VisionUnknown
	}
	if c.Responses == "" {
		c.Responses = VisionUnknown
	}
	if c.StandaloneCompaction == "" {
		c.StandaloneCompaction = VisionUnknown
	}
	if c.ServerCompaction == "" {
		c.ServerCompaction = VisionUnknown
	}
	if c.RenderedInputCount == "" {
		c.RenderedInputCount = VisionUnknown
	}
}

func (p *Profile) ValidateContext() error {
	p.NormalizeContext()
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalid, reason) }
	if p.ContextWindowTokens < 0 || p.MaxOutputTokens < 0 {
		return invalid("token budgets cannot be negative")
	}
	if p.ContextWindowTokens > 0 && p.MaxOutputTokens >= p.ContextWindowTokens {
		return invalid("output budget must be smaller than context capacity")
	}
	for _, value := range []float64{p.CompactionTriggerRatio, p.TextSummaryTargetRatio} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value >= 1 {
			return invalid("context ratios must be between 0 and 1")
		}
	}
	for _, value := range []string{p.Capabilities.Responses, p.Capabilities.StandaloneCompaction, p.Capabilities.ServerCompaction, p.Capabilities.RenderedInputCount, p.Capabilities.ToolCalling} {
		if value != VisionYes && value != VisionNo && value != VisionUnknown {
			return invalid("capabilities must be yes, no or unknown")
		}
	}
	switch p.ContextWindowSource {
	case "manual", "existing_config", "environment", "unknown":
	default:
		return invalid("unsupported context capacity source")
	}
	switch p.APIFormat {
	case APIChat:
		if p.CompactionMode != CompactionOff && p.CompactionMode != CompactionText {
			return invalid("chat_completions supports only off or text_summary")
		}
	case APIResponses:
		if p.Capabilities.Responses != VisionYes {
			return invalid("Responses capability must be confirmed")
		}
		switch p.CompactionMode {
		case CompactionOff:
		case CompactionStandalone:
			if p.Capabilities.StandaloneCompaction != VisionYes {
				return invalid("standalone compaction capability must be confirmed")
			}
		case CompactionServer:
			if p.Capabilities.ServerCompaction != VisionYes {
				return invalid("server compaction capability must be confirmed")
			}
		default:
			return invalid("unsupported Responses compaction mode")
		}
	default:
		return invalid("unsupported api_format")
	}
	return nil
}

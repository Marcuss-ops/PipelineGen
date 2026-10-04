package gencore

import (
	"os"
	"strings"
	"sync"
)

// Segment prompt layouts for the per-segment generation fan-out.
const (
	// SegmentPromptLayoutLegacy is the historical layout: the full segment
	// brief (header + assignment + footer + plain-text contract) is prepended
	// ABOVE the task template, so the per-call prompt diverges from the first
	// per-segment byte and Ollama cannot reuse any KV cache across the
	// fan-out. It is the default: flipping layouts is an operator decision
	// verified against a live canary, not a silent behaviour change.
	SegmentPromptLayoutLegacy = "legacy"

	// SegmentPromptLayoutSharedPrefix orders every call so the SHARED
	// instructions (editorial header + plain-text output contract + task
	// template) come FIRST and the per-segment assignment comes LAST
	// (B3, TODO-pipeline-100x-velocita). Ollama's automatic prompt-prefix
	// cache then reuses the shared run across the segment fan-out: segment
	// N+1 re-evaluates only its assignment suffix instead of the whole
	// prompt. Semantics are preserved — the assignment block is framed as
	// the absolute-priority instruction regardless of its position.
	SegmentPromptLayoutSharedPrefix = "shared-prefix"
)

// EnvSegmentPromptLayout is the operator knob for the per-segment prompt
// layout (PipelineGen-owned PIPELINEGEN_* env namespace).
const EnvSegmentPromptLayout = "PIPELINEGEN_OLLAMA_SEGMENT_PROMPT_LAYOUT"

var (
	segmentLayoutOnce sync.Once
	segmentLayout     string
)

// SegmentPromptLayout returns the effective per-segment prompt layout: the
// env override when it names a known layout, the legacy layout otherwise.
// An unknown value is ignored (legacy) rather than failing every generation.
func SegmentPromptLayout() string {
	segmentLayoutOnce.Do(func() {
		segmentLayout = SegmentPromptLayoutLegacy
		raw := strings.TrimSpace(os.Getenv(EnvSegmentPromptLayout))
		switch raw {
		case SegmentPromptLayoutSharedPrefix:
			segmentLayout = SegmentPromptLayoutSharedPrefix
		case "":
			// keep default
		default:
			// unknown value: keep the legacy layout
		}
	})
	return segmentLayout
}

// resetSegmentLayoutForTest re-arms the once-guard so a test that changes the
// environment observes the new value. Production callers never need this.
func resetSegmentLayoutForTest() {
	segmentLayoutOnce = sync.Once{}
	segmentLayout = ""
}

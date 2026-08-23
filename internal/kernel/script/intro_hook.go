package script

import (
	"fmt"
	"strings"
)

// IntroHookSegmentID is the canonical identifier of the narrative
// introduction segment. It is a normal narrative segment: timeline clips
// may precede and follow its narration, and a direct stock binding may
// accompany its source_text exactly like any other segment.
const IntroHookSegmentID = "intro-hook"

// ValidateIntroHookStock enforces the intro-hook segment contract for
// direct stock bindings. Every binding that targets the intro-hook
// segment must:
//   - point at index 0 and, when scene_id is supplied, at "scene-0";
//   - have a matching first explicit segment whose id is "intro-hook";
//   - declare a non-empty source_text on that segment;
//   - declare a non-empty temporal window (start_ms >= 0, end_ms > start_ms).
//
// Returns human-readable details; an empty slice means the bindings are
// valid. Called from GenerationEnvelopeV2.Validate() so both the HTTP
// handler path and the worker decode path reject invalid mappings.
func validateIntroHookStock(segments []ScriptSegment, bindings []StockBindingInput, ref string) []string {
	var d []string
	for i, binding := range bindings {
		if binding.SegmentID != IntroHookSegmentID {
			continue
		}
		prefix := fmt.Sprintf("%s: stock_bindings[%d]", ref, i)
		if binding.Index != 0 {
			d = append(d, prefix+": intro-hook stock binding must target index 0")
		}
		if binding.SceneID != "" && binding.SceneID != "scene-0" {
			d = append(d, prefix+": intro-hook stock binding must target scene-0")
		}
		if len(segments) == 0 || segments[0].ID != IntroHookSegmentID {
			d = append(d, prefix+": intro-hook stock requires segments[0].id=intro-hook")
		}
		if len(segments) > 0 && strings.TrimSpace(segments[0].SourceText) == "" {
			d = append(d, prefix+": intro-hook stock requires non-empty source_text")
		}
		if binding.StartMs < 0 {
			d = append(d, prefix+": intro-hook stock requires start_ms >= 0")
		}
		if binding.EndMs <= binding.StartMs {
			d = append(d, prefix+": intro-hook stock binding requires end_ms must be greater than start_ms")
		}
	}
	return d
}

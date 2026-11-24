package adapters

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// NormalizeScenesForExplicitSegments creates the exact scene slots declared by
// the caller. Existing generated scenes are retained by position; missing slots
// are grounded in the caller-provided segment source text/topic. This keeps
// direct stock and clip bindings deterministic without inventing empty scenes.
//
// It lives in adapters because both the root stock_bindings processor and the
// voiceover clip_bindings subpackage own this explicit-segment contract.
func NormalizeScenesForExplicitSegments(existing []scriptpkg.SpecScene, segments []scriptpkg.ScriptSegment) []scriptpkg.SpecScene {
	if len(segments) == 0 {
		return existing
	}
	scenes := make([]scriptpkg.SpecScene, len(segments))
	for i, segment := range segments {
		if i < len(existing) {
			scenes[i] = existing[i]
		}
		scenes[i].Index = i
		// Explicit segments define the canonical scene slots. Re-key the
		// retained model scenes by slot so stale/generated IDs cannot leave
		// gaps (for example scene-0, scene-1, scene-3 for index 2).
		scenes[i].ID = fmt.Sprintf("scene-%d", i)
		if strings.TrimSpace(segment.ID) != "" {
			scenes[i].SegmentID = segment.ID
		} else if strings.TrimSpace(scenes[i].SegmentID) == "" {
			scenes[i].SegmentID = fmt.Sprintf("segment-%d", i+1)
		}
		// Explicit source_text is the caller's segment boundary. If the
		// prose synthesizer merged a neighbouring topic, dropped the
		// subject, or produced an unusably short slot, restore that
		// segment's own source text instead of publishing cross-subject
		// narration under the wrong stock binding.
		if explicitSegmentTextNeedsRepair(scenes[i].Text, segment, segments) {
			scenes[i].Text = strings.TrimSpace(segment.SourceText)
		}
		if strings.TrimSpace(scenes[i].Text) == "" {
			scenes[i].Text = strings.TrimSpace(segment.SourceText)
			if scenes[i].Text == "" {
				scenes[i].Text = strings.TrimSpace(segment.Topic)
			}
		}
		// A segment-level Drive folder is the visual source of record. Do not
		// let a stale `kind: clip` label route it through clip rendering; the
		// explicit stock binding owns this scene's video source.
		if strings.TrimSpace(segment.StockFolderID) != "" || strings.TrimSpace(segment.StockFolderLink) != "" {
			scenes[i].Kind = scriptpkg.SceneStock
		} else if kind := scriptpkg.SceneKind(strings.TrimSpace(segment.Kind)); kind.Valid() {
			scenes[i].Kind = kind
		} else if len(segments) > 1 && i == 0 {
			scenes[i].Kind = scriptpkg.SceneIntro
		} else if len(segments) > 1 && i == len(segments)-1 {
			scenes[i].Kind = scriptpkg.SceneOutro
		} else if len(segment.ClipIDs) > 0 {
			scenes[i].Kind = scriptpkg.SceneClip
		} else if !scenes[i].Kind.Valid() {
			scenes[i].Kind = scriptpkg.SceneNarration
		}
	}
	return scenes
}

func explicitSegmentTextNeedsRepair(text string, segment scriptpkg.ScriptSegment, segments []scriptpkg.ScriptSegment) bool {
	if strings.TrimSpace(segment.SourceText) == "" {
		return strings.TrimSpace(text) == ""
	}
	// source_text is an editorial brief, not a minimum narration length.
	// Any non-empty generated narration is authoritative; replacing it based
	// on a universal word threshold can speak the brief verbatim (for example
	// "Explain ..." / "Focus on ...") and leak authoring instructions into
	// TTS. Recovery is limited to an empty result or explicit cross-segment
	// contamination checks below.
	body := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	primary := strings.ToLower(strings.TrimSpace(segment.Topic))
	if strings.Contains(segment.ID, "boxer-") && primary != "" && !strings.Contains(body, primary) {
		return true
	}
	for _, other := range segments {
		if other.ID == segment.ID || !strings.Contains(other.ID, "boxer-") {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(other.Topic))
		if name != "" && strings.Contains(body, name) {
			return true
		}
	}
	return false
}

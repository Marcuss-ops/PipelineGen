// Package script — narration_contract.go owns the ONE word-floor contract for
// narration scenes, shared by every place that can reject a payload or a
// scene: the HTTP ingress (`GenerationEnvelopeV2.Validate`), `velox preflight`
// and the asynchronous runner.
//
// Why it lives here (2026-11-23, Augias r42): the floor used to be a runner
// constant (`minimumClipSceneWords`) applied only after the job had started,
// so a payload with a ten-word narration passed the 400-free ingress and died
// at GENERATING_SCENE_TEXT after the expensive stages had been scheduled. The
// same number in two places is what allowed the drift; the ingress, the CLI and
// the runner now ask this file.
package script

import (
	"strings"
)

// MinimumClipSceneWords is the minimum number of words an authored narration
// scene must carry. Clips cutaways (NoVoiceover) and short authored intro
// lines are deliberately exempt: their only text surface is the footage or the
// producer's fixed copy.
const MinimumClipSceneWords = 12

// NarrationWordFloor returns the minimum word count a scene's narration must
// have, given its segment contract. Zero means "no floor": the segment is a
// fixed-media cutaway or an authored intro whose copy is intentionally short.
func NarrationWordFloor(segment ScriptSegment) int {
	if segment.NoVoiceover {
		return 0
	}
	if strings.EqualFold(strings.TrimSpace(segment.Kind), "intro") {
		return 0
	}
	if segment.MinWords > 0 {
		return segment.MinWords
	}
	return MinimumClipSceneWords
}

// NarrationFloorForScene returns the floor that applies to one scene, matched
// to its segment by id. A scene with no matching segment keeps the canonical
// floor, so an unexpected scene id can never bypass the contract.
func NarrationFloorForScene(segments []ScriptSegment, sceneID string) int {
	id := strings.TrimSpace(sceneID)
	for _, segment := range segments {
		if strings.TrimSpace(segment.ID) != id {
			continue
		}
		return NarrationWordFloor(segment)
	}
	return MinimumClipSceneWords
}

// WordCount returns the narration word count used by the floor. It is the same
// whitespace split the runner applies to scene text, kept in one place so the
// ingress cannot disagree with the runtime about what a word is.
func WordCount(text string) int {
	return len(strings.Fields(text))
}

// ValidateAuthoredNarration reports the verbatim narration segments whose
// authored copy is shorter than the floor. Only verbatim runs are checked:
// without `source_text_verbatim` the scene text is generated from the topic and
// the word budget is enforced later by the prose validator.
func ValidateAuthoredNarration(spec ScriptSpec) []string {
	if !spec.SourceTextVerbatim {
		return nil
	}
	var problems []string
	for _, segment := range spec.Segments {
		text := strings.TrimSpace(segment.SourceText)
		if text == "" {
			// No authored copy: the segment is generated or uses the clip's
			// own transcript, so the static floor does not apply.
			continue
		}
		floor := NarrationWordFloor(segment)
		if floor == 0 {
			continue
		}
		words := WordCount(text)
		if words >= floor {
			continue
		}
		ref := strings.TrimSpace(segment.ID)
		if ref == "" {
			ref = "segment"
		}
		problems = append(problems, ref+": narration has "+itoa(words)+" words, minimum is "+itoa(floor)+" (SCRIPT_SCENE_TEXT_INVALID)")
	}
	return problems
}

// itoa keeps the file dependency-free: strconv is fine, but a one-argument
// wrapper avoids importing it in three call sites of the same message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}

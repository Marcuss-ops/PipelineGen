// Voiceover TTS word-boundary normalization, split out of
// process_segment_publish.go to keep that file under the per-file line cap.
package voiceover

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

const maxFinalWordOverrunUS = int64(750_000)

// normalizeFinalWordTiming keeps the final TTS boundary word when it begins
// just past the encoded audio duration, clamping it to a zero-length span at
// the audio end, then clamps a small overrun on the final word to the measured
// duration. A trailing word whose overrun is larger than
// maxFinalWordOverrunUS is still dropped, so BuildSpeechTimingArtifact fails
// closed instead of publishing inaccurate timestamps.
//
// Dropping the last word made the text/timing token lists differ, so the
// whole-narration phrase anchor for that scene failed and the planner lost
// every phrase/entity surface timed by that voiceover.
func normalizeFinalWordTiming(words []audio.SpeechWordTiming, durationUS int64) ([]audio.SpeechWordTiming, int, int64) {
	if durationUS <= 0 {
		return words, 0, 0
	}
	dropped := 0
	for len(words) > 1 && words[len(words)-1].StartUS >= durationUS {
		last := &words[len(words)-1]
		if last.StartUS-durationUS <= maxFinalWordOverrunUS {
			last.StartUS = durationUS
			last.EndUS = durationUS
			break
		}
		words = words[:len(words)-1]
		dropped++
	}
	var clampedOverrunUS int64
	if len(words) > 0 {
		last := &words[len(words)-1]
		if last.EndUS > durationUS && last.EndUS-durationUS <= maxFinalWordOverrunUS && last.StartUS < durationUS {
			clampedOverrunUS = last.EndUS - durationUS
			last.EndUS = durationUS
		}
	}
	return words, dropped, clampedOverrunUS
}

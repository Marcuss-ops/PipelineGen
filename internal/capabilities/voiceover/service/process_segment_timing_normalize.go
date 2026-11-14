// Voiceover TTS word-boundary normalization, split out of
// process_segment_publish.go to keep that file under the per-file line cap.
package voiceover

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

const maxFinalWordOverrunUS = int64(750_000)

// normalizeFinalWordTiming removes trailing TTS boundary words that begin at
// or after the final encoded audio duration, then clamps a small overrun on
// the new final word to the measured duration. Words are mutated in place and
// indices remain contiguous because only the tail is truncated. Overruns
// larger than maxFinalWordOverrunUS are left untouched so BuildSpeechTimingArtifact
// fails closed rather than publishing inaccurate timestamps.
func normalizeFinalWordTiming(words []audio.SpeechWordTiming, durationUS int64) ([]audio.SpeechWordTiming, int, int64) {
	if durationUS <= 0 {
		return words, 0, 0
	}
	dropped := 0
	for len(words) > 1 && words[len(words)-1].StartUS >= durationUS {
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

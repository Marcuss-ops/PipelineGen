// Voiceover TTS word-boundary normalization, split out of
// process_segment_publish.go to keep that file under the per-file line cap.
package voiceover

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// maxFinalWordOverrunUS bounds how far the provider's final word boundary may
// sit past the encoded audio before the scene fails closed. Providers drift by
// a few hundred milliseconds on the trailing word (a live job failed at
// 777_825us, just past the former 750_000us ceiling); 1.5s covers that drift
// while still rejecting a genuinely misaligned timing.
const maxFinalWordOverrunUS = int64(1_500_000)

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
	// Walk back over EVERY word that ends past the audio, not only the last
	// one: when the final boundary starts after the duration it is collapsed
	// to a zero-length span, and the previous word can still overhang. That
	// second-to-last overhang is what a live s3/en job hit (word 166 ended
	// 83ms past a 66.024s audio) after the trailing word had been collapsed.
	var clampedOverrunUS int64
	for i := len(words) - 1; i >= 0; i-- {
		word := &words[i]
		if word.EndUS <= durationUS {
			// A collapsed trailing word sits exactly at the duration; keep
			// walking back so the word before it is clamped too. A normal word
			// that ends before the audio means the tail is done.
			if word.StartUS < durationUS {
				break
			}
			continue
		}
		overrun := word.EndUS - durationUS
		if overrun > maxFinalWordOverrunUS {
			break
		}
		clampedOverrunUS += overrun
		word.EndUS = durationUS
		if word.StartUS > word.EndUS {
			word.StartUS = word.EndUS
		}
	}
	return words, dropped, clampedOverrunUS
}

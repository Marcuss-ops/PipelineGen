package timeline

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// ValidateAudioSourceRanges is the pre-TTS gate for the canonical audio
// contract. It mirrors the source-range portion of CanonicalTimeline.Validate
// on the raw scenes, so an inverted/out-of-window clip intent fails before
// TTS, clip-audio preparation and the audio render.
//
// A scene with no resolved duration is not rejected here: TTS may legitimately
// provide that duration later. The sign and clip-window checks still run.
func ValidateAudioSourceRanges(scenes []contract.Scene) error {
	for i := range scenes {
		scene := &scenes[i]
		segment := i
		if scene.Index > 0 {
			segment = scene.Index
		}
		durationUS := scene.DurationUS
		if durationUS <= 0 && scene.DurationMS > 0 {
			durationUS = scene.DurationMS * 1000
		}
		intents := scene.AudioIntents
		if len(intents) == 0 {
			if scene.Audio.Mode == "" {
				continue
			}
			intents = []audio.AudioIntent{scene.Audio}
		}
		for _, intent := range intents {
			if intent.Mode == "" {
				continue
			}
			if intent.SourceInUS < 0 || intent.SourceDurationUS < 0 ||
				intent.TimelineOffsetUS < 0 || intent.TimelineDurationUS < 0 {
				return fmt.Errorf("%w: segment %d audio source range", audio.ErrInvalidTimeline, segment)
			}
			if durationUS > 0 && (intent.TimelineOffsetUS > durationUS ||
				intent.TimelineDurationUS > durationUS-intent.TimelineOffsetUS) {
				return fmt.Errorf("%w: segment %d audio source range", audio.ErrInvalidTimeline, segment)
			}
			if intent.Mode == audio.AudioClip && intent.SourceDurationUS <= 0 {
				return fmt.Errorf("%w: segment %d audio source range", audio.ErrInvalidTimeline, segment)
			}
			if intent.ProtectedOriginalAudio &&
				(intent.Mode != audio.AudioClip || !intent.UseOriginalAudio || strings.TrimSpace(intent.ClipAssetID) == "") {
				return fmt.Errorf("%w: segment %d protected audio must be an original clip stream", audio.ErrInvalidTimeline, segment)
			}
			if intent.StockOriginalAudio &&
				(intent.Mode != audio.AudioClip || !intent.UseOriginalAudio || strings.TrimSpace(intent.ClipAssetID) == "") {
				return fmt.Errorf("%w: segment %d stock audio must be an original clip stream", audio.ErrInvalidTimeline, segment)
			}
		}
	}
	return nil
}

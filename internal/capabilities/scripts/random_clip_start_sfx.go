package scriptgeneration

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// randomClipStartSFXIntents creates one deterministic random whoosh at the
// beginning of each real video segment. Synthetic freeze tails are excluded.
// Selecting a bound alias here lets the normal asset resolver handle the cue.
func randomClipStartSFXIntents(timeline audio.CanonicalTimeline) []scriptpkg.SoundEffectIntent {
	intents := make([]scriptpkg.SoundEffectIntent, 0)
	for _, scene := range timeline.Segments {
		for _, clip := range scene.EffectiveVideoSegments() {
			if clip.AssetID == "" || clip.Freeze {
				continue
			}
			startUS := scene.TimelineStartUS + clip.TimelineOffsetUS
			if startUS < 0 || startUS >= timeline.DurationUS || startUS%1000 != 0 {
				continue
			}
			intent := scriptpkg.SoundEffectIntent{AtMS: startUS / 1000, GainDB: -6}
			intent.AssetID = randomWhooshID(timeline, len(intents), intent)
			// Keep the cue short and clearly below the voice/music bed.
			// The canonical SFX bus is -10 dB, so -6 dB here yields -16 dB.
			intent.DurationMS = 250
			intents = append(intents, intent)
		}
	}
	return intents
}

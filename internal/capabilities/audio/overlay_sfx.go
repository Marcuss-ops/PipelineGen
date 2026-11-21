package audio

// overlay_sfx.go owns the last step of the image-bound SFX projection: adding
// already-resolved overlay cues to the sealed master plan.
//
// The overlay plan is projected only AFTER the audio plan has been compiled
// (runner_phase_audio.go), so this method mutates the compiled plan in place,
// appends the cues to the SFX track and seals it again. Timeline, mix policy
// and every other event stay exactly as compiled: the master remains the single
// owner of Voiceover, BGM and SFX.

import (
	"fmt"
)

// AppendSFXLayers appends overlay-derived SFX events to the plan and re-seals
// it. Layers are filtered against the protected fixed-media windows with the
// SAME policy CompileWithLayers uses, so an overlay cue can never bury the
// authoritative original audio. Idempotency is fail-closed: an event id
// already present in the plan is an error, never a silent duplicate.
func (p *CompiledAudioPlan) AppendSFXLayers(timeline CanonicalTimeline, layers []AudioLayer) error {
	if p == nil {
		return fmt.Errorf("append overlay sfx: plan is nil")
	}
	if len(layers) == 0 {
		return nil
	}
	if err := timeline.Validate(); err != nil {
		return fmt.Errorf("append overlay sfx: %w", err)
	}
	layers = filterSFXOutsideProtectedWindows(layers, protectedAudioWindows(timeline))
	if len(layers) == 0 {
		return nil
	}
	existing := make(map[string]struct{})
	for _, track := range p.Tracks {
		for _, event := range track.Events {
			existing[event.EventID] = struct{}{}
		}
	}
	track := findOrCreateLayerTrack(&p.Tracks, TrackSFX, "sfx")
	for index, layer := range layers {
		if layer.TimelineStartUS < 0 || layer.DurationUS <= 0 ||
			layer.TimelineStartUS+layer.DurationUS > timeline.DurationUS {
			return fmt.Errorf("append overlay sfx: cue %d [%d,%d) is outside the %dus timeline",
				index, layer.TimelineStartUS, layer.TimelineStartUS+layer.DurationUS, timeline.DurationUS)
		}
		eventID := fmt.Sprintf("overlay-sfx-%d", index)
		if _, duplicate := existing[eventID]; duplicate {
			return fmt.Errorf("append overlay sfx: event %q is already present in the plan", eventID)
		}
		existing[eventID] = struct{}{}
		track.Events = append(track.Events, AudioEvent{
			EventID:          eventID,
			Type:             EventSFX,
			AssetID:          layer.AssetID,
			TimelineStartUS:  layer.TimelineStartUS,
			DurationUS:       layer.DurationUS,
			SourceInUS:       layer.SourceInUS,
			SourceDurationUS: layer.DurationUS,
			GainDB:           layer.GainDB,
		})
	}
	if err := p.Seal(); err != nil {
		return fmt.Errorf("append overlay sfx: seal plan: %w", err)
	}
	return nil
}

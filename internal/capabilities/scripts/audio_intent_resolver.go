// Package scriptgeneration — audio_intent_resolver.go converts the
// wire-level audio intent commands into fully resolved timeline facts
// BEFORE the compiled plan.
//
// The compiled plan must contain only deterministic, absolute events:
// the renderer (Rust) never derives placement from scene references. This
// resolver is the single boundary that collapses scene-relative SFX
// commands (scene_id + anchor + offset_ms) into absolute microsecond
// offsets and normalizes every millisecond field to canonical
// microseconds, so everything downstream (layer resolver → loop expander
// → CompileWithLayers) works on resolved facts only.
package scriptgeneration

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AudioIntentResolver turns SFX intent commands into resolved placements.
// It is stateless and pure: same timeline + intents → same output, always.
type AudioIntentResolver struct{}

// NewAudioIntentResolver builds the canonical resolver. No dependencies:
// the resolver consumes only the canonical timeline and the wire DTOs.
func NewAudioIntentResolver() *AudioIntentResolver {
	return &AudioIntentResolver{}
}

// ResolveSoundEffects converts every SFX intent into one ResolvedSFX with
// an absolute TimelineStartUS:
//
//   - SceneID == ""  → absolute placement at at_ms (0 = video start);
//   - SceneID != ""  → scene-relative placement resolved against the
//     canonical timeline: anchor "start" (default) / "middle" / "end"
//     plus the signed offset_ms.
//
// Fail-closed: unknown scenes, invalid anchors, dual placement
// (at_ms + scene_id), out-of-timeline placements, negative trims, and a
// missing asset_id all fail the whole resolution.
func (r *AudioIntentResolver) ResolveSoundEffects(timeline audio.CanonicalTimeline, intents []scriptpkg.SoundEffectIntent) ([]audio.ResolvedSFX, error) {
	if err := timeline.Validate(); err != nil {
		return nil, fmt.Errorf("resolve sound effects: %w", err)
	}
	scenes := make(map[string]audio.TimelineSegment, len(timeline.Segments))
	for _, s := range timeline.Segments {
		if _, dup := scenes[s.ID]; dup {
			return nil, fmt.Errorf("resolve sound effects: duplicate scene id %q in canonical timeline", s.ID)
		}
		scenes[s.ID] = s
	}
	out := make([]audio.ResolvedSFX, 0, len(intents))
	for i, intent := range intents {
		assetID := strings.TrimSpace(intent.AssetID)
		if assetID == "" {
			return nil, fmt.Errorf("resolve sound effects: sfx %d requires an asset_id", i)
		}
		if strings.EqualFold(assetID, "random_whoosh") {
			assetID = randomWhooshID(timeline, i, intent)
		}
		startUS, err := resolveSFXStartUS(timeline.DurationUS, scenes, intent)
		if err != nil {
			return nil, fmt.Errorf("resolve sound effects: sfx %d (%s): %w", i, assetID, err)
		}
		sourceInUS := intent.SourceInMS * 1000
		if sourceInUS < 0 {
			return nil, fmt.Errorf("resolve sound effects: sfx %d (%s): source_in_ms must be >= 0", i, assetID)
		}
		durationUS := intent.DurationMS * 1000
		if durationUS < 0 {
			return nil, fmt.Errorf("resolve sound effects: sfx %d (%s): duration_ms must be >= 0", i, assetID)
		}
		if durationUS > 0 && startUS > timeline.DurationUS-durationUS {
			return nil, fmt.Errorf("resolve sound effects: sfx %d (%s): placement [%d,%d) exceeds the %dus timeline", i, assetID, startUS, startUS+durationUS, timeline.DurationUS)
		}
		out = append(out, audio.ResolvedSFX{
			AssetID:         assetID,
			TimelineStartUS: startUS,
			SourceInUS:      sourceInUS,
			DurationUS:      durationUS,
			GainDB:          intent.GainDB,
		})
	}
	return out, nil
}

// randomWhooshID selects from the nine local whoosh assets without involving
// the renderer or filesystem. The selection is deterministic for a timeline
// and event position, so retries remain idempotent while different events
// normally receive different cues.
func randomWhooshID(timeline audio.CanonicalTimeline, index int, intent scriptpkg.SoundEffectIntent) string {
	seed := fmt.Sprintf("%d:%d:%d:%s:%d:%d", timeline.DurationUS, index, intent.AtMS, intent.SceneID, intent.OffsetMS, intent.SourceInMS)
	digest := digest.SHA256Bytes([]byte(seed))
	return fmt.Sprintf("whoosh%d", int(digest[0])%9+1)
}

// resolveSFXStartUS collapses one intent's placement command into an
// absolute microsecond offset within [0, timelineDurationUS).
func resolveSFXStartUS(timelineDurationUS int64, scenes map[string]audio.TimelineSegment, intent scriptpkg.SoundEffectIntent) (int64, error) {
	offsetUS := intent.OffsetMS * 1000
	if intent.SceneID == "" {
		// Absolute placement: at_ms is the offset (0 = video start).
		if strings.TrimSpace(string(intent.Anchor)) != "" {
			return 0, fmt.Errorf("anchor requires a scene_id")
		}
		if offsetUS != 0 {
			return 0, fmt.Errorf("offset_ms requires a scene_id")
		}
		start := intent.AtMS * 1000
		if start < 0 {
			return 0, fmt.Errorf("at_ms must be >= 0")
		}
		if start >= timelineDurationUS {
			return 0, fmt.Errorf("absolute placement at %dus is outside the %dus timeline", start, timelineDurationUS)
		}
		return start, nil
	}
	if intent.AtMS != 0 {
		return 0, fmt.Errorf("at_ms and scene_id are mutually exclusive (dual placement)")
	}
	scene, ok := scenes[intent.SceneID]
	if !ok {
		return 0, fmt.Errorf("unknown scene %q", intent.SceneID)
	}
	anchor, err := intent.Anchor.Normalize()
	if err != nil {
		return 0, err
	}
	var start int64
	switch anchor {
	case scriptpkg.SFXAnchorStart:
		start = scene.TimelineStartUS + offsetUS
	case scriptpkg.SFXAnchorMiddle:
		start = scene.TimelineStartUS + scene.DurationUS/2 + offsetUS
	case scriptpkg.SFXAnchorEnd:
		start = scene.TimelineStartUS + scene.DurationUS + offsetUS
	}
	// The RESULT is validated against the whole timeline, not the scene:
	// a negative offset on "start" may legitimately land in the previous
	// scene (whoosh before a transition), and a positive offset on "end"
	// may bleed into the next one.
	if start < 0 {
		return 0, fmt.Errorf("scene %q %s placement lands at %dus before the timeline start", intent.SceneID, anchor, start)
	}
	if start >= timelineDurationUS {
		return 0, fmt.Errorf("scene %q %s placement lands at %dus beyond the %dus timeline", intent.SceneID, anchor, start, timelineDurationUS)
	}
	return start, nil
}

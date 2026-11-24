package contract

import (
	"fmt"
	"strings"
)

// EditingTimebase declares the time unit used throughout the editing
// timeline. All timestamps are integer microseconds.
const EditingTimebase = "us"

// EditingTimelineVersion is the schema version of the editing timeline.
const EditingTimelineVersion = "v1"

// editingAudioPacketDurationToleranceUS permits the final AAC packet to
// extend a few milliseconds beyond the frame-aligned video timeline. AAC is
// encoded in 1024-sample packets; at 48 kHz one packet is 21,334 µs. The
// timeline keeps the certified asset duration while validating this bounded
// mux padding separately from actual scene drift.
const editingAudioPacketDurationToleranceUS int64 = 21_334

// EditingTimelineV1 is the canonical editing projection. Its video duration
// covers the canonical timeline and any longer overlay tail; audio keeps its
// independently certified duration and may end before that visual tail.
type EditingTimelineV1 struct {
	Version    string               `json:"version"`
	Timebase   string               `json:"timebase"`
	DurationUS int64                `json:"duration_us"`
	Audio      EditingAudioRef      `json:"audio"`
	Scenes     []EditingSceneSpan   `json:"scenes"`
	Overlays   []EditingOverlaySpan `json:"overlays"`
}

// EditingAudioRef is the final audio asset reference in the editing
// timeline. Carries the asset identity and SHA256 for integrity verification.
type EditingAudioRef struct {
	AssetID    string `json:"asset_id"`
	DriveLink  string `json:"drive_link,omitempty"`
	SHA256     string `json:"sha256"`
	DurationUS int64  `json:"duration_us"`
}

// EditingSceneSpan is one scene's time span on the final combined timeline.
type EditingSceneSpan struct {
	SceneID string `json:"scene_id"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

// EditingOverlaySpan is one overlay artifact's time span on the final
// combined timeline. StartUS/EndUS come from the same certified surfaces
// as the OverlayPlan items.
//
// The span also carries the artifact lineage that proves the composed
// contains THIS overlay: the overlay.render queue job id, the frozen plan
// fingerprint, the item's render key, and the source video asset the overlay
// is composited over. Downstream editing reads this block (never a second
// derivation) to trace one rendered overlay from its scene back to the exact
// queue job and plan version that produced it.
type EditingOverlaySpan struct {
	ArtifactID string `json:"artifact_id"`
	SceneID    string `json:"scene_id"`
	Entity     string `json:"entity"`
	TemplateID string `json:"template_id"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DriveLink  string `json:"drive_link,omitempty"`
	SHA256     string `json:"sha256"`
	// MediaContract is the ID of the OverlayMediaContract the rendered
	// artifact was certified against (container/codec/pixel format,
	// audio_streams==0). It comes from the frozen OverlayPlan, never from a
	// second independent derivation.
	MediaContract string `json:"media_contract,omitempty"`
	// RenderJobID is the overlay.render queue job id that produced the
	// rendered artifact (the Run's OverlayRender.JobID). Empty until the
	// render has completed.
	RenderJobID string `json:"render_job_id,omitempty"`
	// PlanFingerprint is the frozen OverlayPlan fingerprint the render was
	// validated against (plan fingerprint == result fingerprint).
	PlanFingerprint string `json:"plan_fingerprint,omitempty"`
	// RenderKey is the item's content-addressed render key, the join key
	// between the plan item and its rendered artifact.
	RenderKey string `json:"render_key,omitempty"`
	// SourceVideoAssetID is the video asset the overlay is composited over
	// (the OverlayPlan's VideoID).
	SourceVideoAssetID string `json:"source_video_asset_id,omitempty"`
	// IntentID is the pre-timing OverlayIntent id this overlay materialized
	// from (the "overlay_intent_id" hop of the artifact lineage). It is
	// looked up from the run's own OverlayIntents by (scene, entity) — never
	// re-derived. Empty when no intent matches the item.
	IntentID string `json:"intent_id,omitempty"`
	// IntentFingerprint is the content fingerprint of that OverlayIntent,
	// proving which intent version the plan item was bound from.
	IntentFingerprint string `json:"intent_fingerprint,omitempty"`
}

// Validate checks structural invariants on the editing timeline.
func (t EditingTimelineV1) Validate() error {
	if t.Version != EditingTimelineVersion {
		return fmt.Errorf("editing timeline: unsupported version %q", t.Version)
	}
	if t.Timebase != EditingTimebase {
		return fmt.Errorf("editing timeline: unsupported timebase %q", t.Timebase)
	}
	if t.DurationUS <= 0 {
		return fmt.Errorf("editing timeline: duration_us must be positive")
	}
	if strings.TrimSpace(t.Audio.AssetID) == "" {
		return fmt.Errorf("editing timeline: audio asset_id is required")
	}
	if strings.TrimSpace(t.Audio.SHA256) == "" {
		return fmt.Errorf("editing timeline: audio sha256 is required")
	}
	if t.Audio.DurationUS-t.DurationUS > editingAudioPacketDurationToleranceUS {
		return fmt.Errorf("editing timeline: audio duration %d exceeds timeline duration %d",
			t.Audio.DurationUS, t.DurationUS)
	}
	return t.ValidateSpans()
}

// validateSpans also serves pre-encode validation without inventing a certified
// audio reference; identity and duration certification remain finalize gates.
func (t EditingTimelineV1) ValidateSpans() error {
	// Scene spans must be non-overlapping and within duration.
	seenScenes := make(map[string]struct{}, len(t.Scenes))
	var prevEnd int64
	for i, scene := range t.Scenes {
		if strings.TrimSpace(scene.SceneID) == "" {
			return fmt.Errorf("editing timeline: scene[%d] scene_id is required", i)
		}
		if _, dup := seenScenes[scene.SceneID]; dup {
			return fmt.Errorf("editing timeline: duplicate scene %q", scene.SceneID)
		}
		seenScenes[scene.SceneID] = struct{}{}
		if scene.StartUS < 0 || scene.EndUS <= scene.StartUS {
			return fmt.Errorf("editing timeline: scene %q has invalid time range [%d,%d)",
				scene.SceneID, scene.StartUS, scene.EndUS)
		}
		if scene.EndUS > t.DurationUS {
			return fmt.Errorf("editing timeline: scene %q end %d past duration %d",
				scene.SceneID, scene.EndUS, t.DurationUS)
		}
		if scene.StartUS < prevEnd {
			return fmt.Errorf("editing timeline: scene %q start %d overlaps previous end %d",
				scene.SceneID, scene.StartUS, prevEnd)
		}
		prevEnd = scene.EndUS
	}
	// Overlay spans must be within duration.
	for i, overlay := range t.Overlays {
		if strings.TrimSpace(overlay.ArtifactID) == "" {
			return fmt.Errorf("editing timeline: overlay[%d] artifact_id is required", i)
		}
		if overlay.StartUS < 0 || overlay.EndUS <= overlay.StartUS {
			return fmt.Errorf("editing timeline: overlay %q has invalid time range [%d,%d)",
				overlay.ArtifactID, overlay.StartUS, overlay.EndUS)
		}
		if overlay.EndUS > t.DurationUS {
			return fmt.Errorf("editing timeline: overlay %q end %d past duration %d",
				overlay.ArtifactID, overlay.EndUS, t.DurationUS)
		}
	}
	return nil
}

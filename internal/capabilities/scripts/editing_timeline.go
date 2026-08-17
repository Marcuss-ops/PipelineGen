// Package scriptgeneration — editing_timeline.go owns the canonical
// EditingTimelineV1 projection: a single JSON document that bundles the
// final audio identity, scene spans, and overlay artifact references — all
// in integer microseconds — so downstream editing has one authoritative
// timing surface.
//
// EditingTimelineV1 is a READ-ONLY projection of frozen canonical facts:
//   - CanonicalTimeline → scene spans (start_us / end_us)
//   - FinalAudio → audio identity (asset_id, SHA256, duration)
//   - OverlayPlan + EntityTimeline → overlay artifact spans
//
// It is never an independent timing source. Every timestamp is derived
// from the same certified surfaces the rest of the pipeline consumes.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// EditingTimelineVersion is the schema version of the editing timeline.
const EditingTimelineVersion = "v1"

// EditingTimebase declares the time unit used throughout the editing
// timeline. All timestamps are integer microseconds.
const EditingTimebase = "us"

// EditingTimelineV1 is the canonical editing projection. One JSON, one
// timebase, built from frozen facts. Downstream editing consumes this
// document and nothing else for timing decisions.
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
type EditingOverlaySpan struct {
	ArtifactID string `json:"artifact_id"`
	SceneID    string `json:"scene_id"`
	Entity     string `json:"entity"`
	TemplateID string `json:"template_id"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DriveLink  string `json:"drive_link,omitempty"`
	SHA256     string `json:"sha256"`
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
	if t.Audio.DurationUS != t.DurationUS {
		return fmt.Errorf("editing timeline: audio duration %d does not match timeline duration %d",
			t.Audio.DurationUS, t.DurationUS)
	}
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

// BuildEditingTimeline projects the frozen canonical facts into an
// EditingTimelineV1. Returns nil when the result carries no timeline or
// audio (legitimate no-op for NONE/CHUNKED modes).
//
// Overlay times come from the OverlayPlan items when available (millisecond
// items converted to microseconds); when the EntityTimeline is also
// available, its microsecond-precision AudioStartUS/AudioEndUS are preferred
// for entity card overlays.
func BuildEditingTimeline(result *GenerateResult) (*EditingTimelineV1, error) {
	if result == nil || result.CanonicalTimeline == nil || result.FinalAudio == nil {
		return nil, nil
	}
	tl := result.CanonicalTimeline
	audio := result.FinalAudio

	// When the final audio has not been certified (duration == 0), the
	// editing timeline cannot be projected. Return nil so downstream can
	// retry after certification completes.
	if audio.DurationUS == 0 {
		return nil, nil
	}

	// Build scene spans from the canonical timeline segments.
	scenes := make([]EditingSceneSpan, 0, len(tl.Segments))
	for _, seg := range tl.Segments {
		scenes = append(scenes, EditingSceneSpan{
			SceneID: seg.ID,
			StartUS: seg.TimelineStartUS,
			EndUS:   seg.TimelineStartUS + seg.DurationUS,
		})
	}

	// Build overlay spans from the overlay plan (when available).
	var overlays []EditingOverlaySpan
	if result.OverlayPlan != nil {
		overlays = overlaysFromPlan(result)
	}

	et := &EditingTimelineV1{
		Version:    EditingTimelineVersion,
		Timebase:   EditingTimebase,
		DurationUS: tl.DurationUS,
		Audio: EditingAudioRef{
			AssetID:    audio.AssetID,
			DriveLink:  audio.DriveLink,
			SHA256:     audio.FinalAudioSHA256,
			DurationUS: audio.DurationUS,
		},
		Scenes:   scenes,
		Overlays: overlays,
	}
	if err := et.Validate(); err != nil {
		return nil, fmt.Errorf("editing timeline: %w", err)
	}
	return et, nil
}

// overlaysFromPlan projects overlay items onto editing overlay spans.
// When the EntityTimeline is available, entity card overlays use its
// microsecond-precision timing instead of the plan's millisecond items.
func overlaysFromPlan(result *GenerateResult) []EditingOverlaySpan {
	plan := result.OverlayPlan
	if plan == nil {
		return nil
	}

	// Build entity timeline lookup for microsecond-precision timing.
	occByEntityID := map[string]struct {
		startUS int64
		endUS   int64
	}{}
	if result.EntityTimeline != nil {
		for _, scene := range result.EntityTimeline.Scenes {
			for _, occ := range scene.Entities {
				occByEntityID[occ.EntityID] = struct {
					startUS int64
					endUS   int64
				}{occ.AudioStartUS, occ.AudioEndUS}
			}
		}
	}

	overlays := make([]EditingOverlaySpan, 0, len(plan.Items))
	for _, item := range plan.Items {
		startUS := item.StartMs * 1000
		endUS := item.EndMs * 1000

		// Entity card overlays: prefer EntityTimeline microsecond timing.
		if item.EntityID != "" {
			if occ, ok := occByEntityID[item.EntityID]; ok {
				startUS = occ.startUS
				endUS = occ.endUS
			}
		}

		overlays = append(overlays, EditingOverlaySpan{
			ArtifactID: item.ID,
			SceneID:    item.SceneID,
			Entity:     item.Text,
			TemplateID: item.TemplateID,
			StartUS:    startUS,
			EndUS:      endUS,
		})
	}
	return overlays
}

// OverlayArtifactRef is the reference to a rendered overlay artifact
// that gets included in the editing timeline. It carries the identity,
// timing, and Drive publication references.
type OverlayArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	SceneID    string `json:"scene_id"`
	TemplateID string `json:"template_id"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DriveLink  string `json:"drive_link,omitempty"`
	SHA256     string `json:"sha256"`
	// MediaContract is the contract ID this artifact was validated against.
	MediaContract string `json:"media_contract"`
}

// EditingOverlayPlanItem returns an OverlayItem-compatible projection of
// an editing overlay span, suitable for consumption by downstream editing
// systems that need the OverlayItem shape with microsecond timing.
func EditingOverlayPlanItem(span EditingOverlaySpan) capabilityoverlay.OverlayItem {
	return capabilityoverlay.OverlayItem{
		ID:         span.ArtifactID,
		SceneID:    span.SceneID,
		Kind:       "entity_card",
		TemplateID: span.TemplateID,
		Text:       span.Entity,
		StartMs:    span.StartUS / 1000,
		EndMs:      (span.EndUS + 999) / 1000,
	}
}

// planOverlayIntents projects per-scene entity annotations onto the neutral
// input types the overlay planner consumes, then calls PlanOverlayIntents.
// It is the bridge between the domain Scene.Annotations (AnnotatedEntity)
// and the overlay planner's EntityOverlayInput. Nil-safe: returns nil when
// the result has no scenes with annotations.
func planOverlayIntents(scenes []Scene, registry *capabilityoverlay.ChrononOverlayRegistry) []capabilityoverlay.OverlayIntent {
	var inputs []capabilityoverlay.SceneEntityInput
	for i, scene := range scenes {
		if scene.Annotations == nil {
			continue
		}
		var entities []capabilityoverlay.EntityOverlayInput
		for _, entity := range scene.Annotations.PrimaryEntities {
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				continue
			}
			entities = append(entities, capabilityoverlay.EntityOverlayInput{
				Name:       name,
				Type:       strings.TrimSpace(entity.Type),
				Confidence: entity.Confidence,
			})
		}
		for _, entity := range scene.Annotations.SecondaryEntities {
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				continue
			}
			entities = append(entities, capabilityoverlay.EntityOverlayInput{
				Name:       name,
				Type:       strings.TrimSpace(entity.Type),
				Confidence: entity.Confidence,
			})
		}
		var phrases []capabilityoverlay.OverlayAnnotationInput
		for _, phrase := range scene.Annotations.ImportantPhrases {
			phrases = append(phrases, capabilityoverlay.OverlayAnnotationInput{ID: phrase.ID, Text: phrase.Text})
		}
		var words []capabilityoverlay.OverlayAnnotationInput
		for _, word := range scene.Annotations.ImportantWords {
			words = append(words, capabilityoverlay.OverlayAnnotationInput{ID: word.ID, Text: word.Text})
		}
		inputs = append(inputs, capabilityoverlay.SceneEntityInput{
			SceneID:    scene.ID,
			SceneIndex: i,
			Entities:   entities,
			Phrases:    phrases,
			Words:      words,
		})
	}
	return capabilityoverlay.PlanOverlayIntents(inputs, registry)
}

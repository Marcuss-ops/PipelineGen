package entities

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MinEntityOverlayDurationUS is the minimum renderable duration for an
// entity preset. The spoken occurrence still anchors the start exactly; the
// visual card remains on screen after the name finishes so a preset render is
// a real clip rather than a sub-second flash.
const MinEntityOverlayDurationUS int64 = 5_000_000

// ResolveEntityOverlayPlan is the OverlayResolver: it turns the canonical
// EntityTimeline into the semantic OverlayPlan the rendering layer consumes.
// Every entity occurrence becomes one entity_card item whose start/end are
// the occurrence's certified global audio positions — the resolver never
// guesses WHEN to show a person, an organization or a place; it shows them
// exactly while they are being spoken. It is the unlimited variant: every
// occurrence resolves (see ResolveRankedEntityOverlayPlan for the ranked,
// per-scene-capped planner path).
//
// Times cross the microsecond→millisecond boundary deterministically: the
// start is floor(us/1000) and the end is ceil(us/1000), so the millisecond
// item strictly covers the certified microsecond span. The plan is sealed
// (render keys + fingerprint) by its own Validate, so the caller can enqueue
// it directly.
func ResolveEntityOverlayPlan(timeline EntityTimeline, planID, videoID, projectID string, width, height, fpsNum, fpsDen int) (capabilityoverlay.OverlayPlan, error) {
	return ResolveRankedEntityOverlayPlan(timeline, planID, videoID, projectID, width, height, fpsNum, fpsDen, RankConfig{})
}

// ResolveRankedEntityOverlayPlan is the ranked OverlayResolver: it turns the
// canonical EntityTimeline into the semantic OverlayPlan, ranking the entity
// occurrences of every scene by the canonical importance score (see
// ImportanceScore) and applying the per-scene caps of cfg. With a zero cfg
// it is byte-identical to ResolveEntityOverlayPlan (no ranking, no caps).
//
// The plan's editorial rule: PipelineGen decides WHO is important — a scene
// never renders every extracted entity. The top-N occurrences by importance
// survive per scene (cfg.MaxEntityOverlaysPerScene); the survivors keep
// their certified timeline positions exactly like the unlimited resolver.
func ResolveRankedEntityOverlayPlan(timeline EntityTimeline, planID, videoID, projectID string, width, height, fpsNum, fpsDen int, cfg RankConfig) (capabilityoverlay.OverlayPlan, error) {
	if err := timeline.Validate(); err != nil {
		return capabilityoverlay.OverlayPlan{}, err
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: plan_id and video_id are required")
	}
	if width <= 0 || height <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: width, height and frame rate must be positive")
	}

	// Run-level context: frequency / novelty / asset quality are derived
	// from the whole timeline, never per scene.
	allOccurrences := allOccurrences(timeline)
	ctx := NewRankContext(allOccurrences)

	var items []capabilityoverlay.OverlayItem
	for _, scene := range timeline.Scenes {
		ranked := RankScene(scene.Entities, ctx, cfg)
		for _, rankedOccurrence := range ranked {
			occurrence := rankedOccurrence.Occurrence
			durationUS := occurrence.AudioEndUS - occurrence.AudioStartUS
			if durationUS < MinEntityOverlayDurationUS {
				durationUS = MinEntityOverlayDurationUS
			}
			endUS := occurrence.AudioStartUS + durationUS
			kind := capabilityoverlay.EntityTypeToKind(occurrence.Type)
			entry, err := capabilityoverlay.DefaultChrononOverlayRegistry.Resolve(string(kind))
			if err != nil {
				return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
			}
			items = append(items, capabilityoverlay.OverlayItem{
				ID:         overlayItemID(occurrence),
				SceneID:    occurrence.SceneID,
				EntityID:   occurrence.EntityID,
				Kind:       string(kind),
				StartMs:    occurrence.AudioStartUS / 1000,
				EndMs:      (endUS + 999) / 1000,
				StartUS:    occurrence.AudioStartUS,
				DurationUS: durationUS,
				TemplateID: entry.Template,
				PresetID:   capabilityoverlay.SelectEntityNamePreset(planID, occurrence.SceneID, overlayItemID(occurrence), occurrence.Type),
				Text:       occurrence.Name,
				// The plan's entity_ref: RenderingGen receives WHO the overlay is
				// about (stable content-addressed id + type + canonical name +
				// surface text), never a bare name.
				EntityRef: &capabilityoverlay.OverlayEntityRef{
					EntityID:    occurrence.EntityID,
					Type:        occurrence.Type,
					Name:        occurrence.Name,
					SurfaceText: occurrence.Name,
				},
			})
		}
	}
	if len(items) == 0 {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: timeline carries no entity occurrences")
	}

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     strings.TrimSpace(projectID),
		Width:         width,
		Height:        height,
		FPSNum:        fpsNum,
		FPSDen:        fpsDen,
		Items:         items,
	}
	if err := plan.Validate(); err != nil {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
	}
	return plan, nil
}

// specialNamePreset is the single PipelineGen editorial choice for entity
// name treatments. The ids are owned by Chronon's VisualPresetRegistry and
// are transported opaquely through the overlay contract to RenderingGen.

// allOccurrences flattens every scene's occurrences into one slice, in scene
// order, for the run-level ranking context.
func allOccurrences(timeline EntityTimeline) []EntityOccurrence {
	var out []EntityOccurrence
	for _, scene := range timeline.Scenes {
		out = append(out, scene.Entities...)
	}
	return out
}

// overlayItemID derives the deterministic, collision-free overlay id for one
// occurrence: "overlay-" + scene id + "-" + safe slug of the entity name
// (e.g. "overlay-scene-3-tom-hanks"). The slug comes from the canonical
// name (SafeEntityID), NOT the content-addressed StableEntityID, so overlay
// ids stay human-readable and stable across runs while EntityID carries the
// dedup/cache identity.
func overlayItemID(o EntityOccurrence) string {
	scene := strings.TrimSpace(o.SceneID)
	entity := SafeEntityID(o.Name)
	if scene == "" {
		return "overlay-" + entity
	}
	return "overlay-" + scene + "-" + entity
}

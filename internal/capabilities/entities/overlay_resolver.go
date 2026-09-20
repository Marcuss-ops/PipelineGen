package entities

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MaxEntityOverlayDurationUS is the hard duration ceiling for entity image
// overlays. A long spoken mention cannot make the image layer run indefinitely.
const MaxEntityOverlayDurationUS int64 = 5_000_000

// MinEntityOverlayDurationUS is retained as the historical five-second
// display floor for text entity cards.
const MinEntityOverlayDurationUS int64 = MaxEntityOverlayDurationUS

// ResolveEntityOverlayPlan is the OverlayResolver: it turns the canonical
// EntityTimeline into the semantic OverlayPlan the rendering layer consumes.
// Every distinct entity in a scene becomes one entity_card item whose
// start/end are the first ranked occurrence's certified global audio
// positions — the resolver never guesses WHEN to show a person, an
// organization or a place. Repeated mentions of the same entity in one scene
// share the same semantic overlay identity, so only the highest-ranked
// occurrence is retained. It is the unlimited variant for distinct entities
// (see ResolveRankedEntityOverlayPlan for the ranked, per-scene-capped path).
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
// never renders every extracted entity. Distinct entities are ranked by their
// highest-scoring occurrence, then the top-N survive per scene
// (cfg.MaxEntityOverlaysPerScene) with that occurrence's certified timing.
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
		// Apply the resolver's cap after semantic deduplication. Passing it into
		// RankScene would let repeated mentions of one entity consume several
		// slots and crowd out distinct people or places in the same scene.
		rankConfig := cfg
		rankConfig.MaxEntityOverlaysPerScene = 0
		ranked := RankScene(scene.Entities, ctx, rankConfig)
		// seenOverlayIdentities is keyed by the overlay ID, not by the entity
		// hash. An entity can be grounded at several word spans in one scene,
		// AND the same entity can reach the pipeline under more than one
		// Unicode spelling — the typographic apostrophe of "Cus D’Amato"
		// versus the ASCII one of "Cus D'Amato". Those spellings hash to
		// DIFFERENT entity ids (the canonical key keeps the raw rune) but
		// collapse to ONE human-readable overlay id, because SafeEntityID
		// slugs every non-alphanumeric rune away. Deduplicating on the entity
		// hash alone therefore emitted two items carrying the same id and the
		// plan failed validation with `duplicate item id`. The overlay
		// identity is the id, so one entity yields exactly one card.
		seenOverlayIdentities := make(map[string]struct{}, len(ranked))
		for _, rankedOccurrence := range ranked {
			occurrence := rankedOccurrence.Occurrence
			// Keep the highest-ranked mention (ties retain RankScene's stable
			// source order) so duplicates do not produce repeated cards.
			identity := overlayItemID(occurrence)
			if _, duplicate := seenOverlayIdentities[identity]; duplicate {
				continue
			}
			if cfg.MaxEntityOverlaysPerScene > 0 && len(seenOverlayIdentities) >= cfg.MaxEntityOverlaysPerScene {
				break
			}
			seenOverlayIdentities[identity] = struct{}{}
			durationUS := occurrence.AudioEndUS - occurrence.AudioStartUS
			if durationUS < MinEntityOverlayDurationUS {
				durationUS = MinEntityOverlayDurationUS
			}
			if durationUS > MaxEntityOverlayDurationUS {
				durationUS = MaxEntityOverlayDurationUS
			}
			startMS := occurrence.AudioStartUS / 1000
			// Overlay transport is millisecond-based. Quantize the optional
			// microsecond projection to that same boundary so the exact
			// five-second editorial window remains internally consistent.
			startUS := startMS * 1000
			kind := capabilityoverlay.EntityTypeToKind(occurrence.Type)
			entry, err := capabilityoverlay.DefaultChrononOverlayRegistry.Resolve(string(kind))
			if err != nil {
				return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
			}
			items = append(items, capabilityoverlay.OverlayItem{
				ID:       identity,
				SceneID:  occurrence.SceneID,
				EntityID: occurrence.EntityID,
				Kind:     string(kind),
				StartMs:  startMS,
				// Entity image/card windows are an editorial five-second
				// contract. Derive the millisecond end from the same floored
				// start so sub-millisecond source timing cannot turn 5s into
				// 5001ms on the wire.
				EndMs:         startMS + durationUS/1000,
				StartUS:       startUS,
				DurationUS:    durationUS,
				TemplateID:    entry.Template,
				PresetID:      capabilityoverlay.SelectEntityNamePreset(planID, occurrence.SceneID, identity, occurrence.Type),
				ImagePresetID: capabilityoverlay.SelectEntityImagePreset(planID, occurrence.SceneID, identity),
				Text:          occurrence.Name,
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
	// SafeEntityID intentionally emits filesystem/Drive-safe ASCII slugs. For
	// scripts whose localized surface is written in Cyrillic (or another
	// non-ASCII alphabet) that slug can be empty for every person in a scene,
	// which would make distinct overlays look identical and silently drop all
	// but the first one. The occurrence's stable content address is the
	// canonical collision-free fallback; it is already part of the entity
	// timeline and does not depend on display-language spelling.
	if entity == "" {
		entity = strings.TrimPrefix(strings.TrimSpace(o.EntityID), "ent_")
	}
	if scene == "" {
		return "overlay-" + entity
	}
	return "overlay-" + scene + "-" + entity
}

package entities

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MaxEntityOverlayDurationUS is the hard editorial ceiling for entity image
// overlays. A long spoken mention cannot make the image layer run indefinitely.
const MaxEntityOverlayDurationUS int64 = 5_000_000

// MinEntityOverlayDurationUS is the readability floor of the dynamic entity
// window: a very short spoken mention still stays on screen a full second so
// the card never collapses into a sub-second flash.
const MinEntityOverlayDurationUS int64 = 1_000_000

// EntityOverlayHoldoutUS is the tail hold added after the last spoken word of
// a mention so the card remains readable once the narration moves on.
const EntityOverlayHoldoutUS int64 = 500_000

// EntitySpokenWindowDuration derives the DYNAMIC entity-overlay duration from
// the certified spoken window of one mention: the audio the entity actually
// occupies plus a half-second readability hold, clamped into
// [MinEntityOverlayDurationUS, MaxEntityOverlayDurationUS]. The duration is
// never estimated from text length or scene duration — only the certified
// audio positions count. A short name gets a readable second; a longer
// narration keeps the card up while it is spoken (bounded by the ceiling).
//
// The result is quantized UP to whole milliseconds: the overlay wire contract
// derives end_ms from start_us+duration_us by ceiling division, so a duration
// that is not a millisecond multiple would make the millisecond and
// microsecond projections diverge and the sealed plan would fail validation.
func EntitySpokenWindowDuration(audioStartUS, audioEndUS int64) int64 {
	spoken := audioEndUS - audioStartUS
	if spoken < 0 {
		spoken = 0
	}
	duration := spoken + EntityOverlayHoldoutUS
	if duration < MinEntityOverlayDurationUS {
		duration = MinEntityOverlayDurationUS
	}
	if duration > MaxEntityOverlayDurationUS {
		duration = MaxEntityOverlayDurationUS
	}
	return ((duration + 999) / 1000) * 1000
}

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
			// Dynamic duration: the card lasts as long as the certified mention
			// plus the readability hold, clamped to the editorial window. No
			// fixed five-second reset: the display follows the spoken timing.
			durationUS := EntitySpokenWindowDuration(occurrence.AudioStartUS, occurrence.AudioEndUS)
			startMS := occurrence.AudioStartUS / 1000
			// Overlay transport is millisecond-based. Quantize the optional
			// microsecond projection to that same boundary so the dynamic
			// editorial window remains internally consistent.
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
				// Entity image/card windows are DYNAMIC: they follow the certified
				// spoken window of the mention (clamped into the editorial bounds).
				// The millisecond end must be the CEILING of start_us+duration_us
				// — the exact projection the overlay plan validator enforces
				// (floor start, ceil end) — so a 500 µs remainder cannot fail the
				// run at COMPILING_AUDIO with a 1 ms divergence.
				EndMs:         overlayEndMs(startUS, durationUS),
				StartUS:       startUS,
				DurationUS:    durationUS,
				TemplateID:    entry.Template,
				PresetID:      capabilityoverlay.SelectEntityNamePreset(planID, occurrence.SceneID, identity, occurrence.Type),
				ImagePresetID: capabilityoverlay.SelectEntityImagePreset(planID, occurrence.SceneID, identity),
				// The name card states its own entrance motion. Leaving it empty
				// would let the compiler fall back to the preset's motion, which on
				// the installed catalog is the glyph-level apple_phrase_v2 stack the
				// native text lane rejects (see SelectTextMotion).
				MotionID: capabilityoverlay.SelectTextMotion(planID, occurrence.SceneID, identity),
				Text:     occurrence.Name,
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

// overlayEndMs projects a canonical microsecond endpoint onto the millisecond
// wire interval without truncating its final partial millisecond.
func overlayEndMs(startUS, durationUS int64) int64 {
	return (startUS + durationUS + 999) / 1000
}

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

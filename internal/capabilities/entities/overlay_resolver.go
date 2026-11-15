package entities

import (
	"context"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MaxEntityOverlayDurationUS is the hard editorial ceiling for entity image
// overlays. A long spoken mention cannot make the image layer run indefinitely.
const MaxEntityOverlayDurationUS int64 = 5_000_000

// MinEntityOverlayDurationUS is the readability floor of the dynamic entity
// window: a very short spoken mention still stays on screen long enough to
// read the name and caption instead of collapsing into a flash.
const MinEntityOverlayDurationUS int64 = 1_000_000

// EntityOverlayHoldoutUS is the tail hold added after the last spoken word of
// a mention so the card remains readable once the narration moves on.
const EntityOverlayHoldoutUS int64 = 500_000

// EntitySpokenWindowDuration derives the DYNAMIC entity-overlay duration from
// the certified spoken window of one mention: the audio the entity actually
// occupies plus a half-second readability hold, clamped into
// [MinEntityOverlayDurationUS, MaxEntityOverlayDurationUS]. The duration is
// never estimated from text length or scene duration — only the certified
// audio positions count. A short name gets a readable beat; a longer
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

// ResolveEntityOverlayPlanWithRanker builds entity render DTOs after
// BrainRust ranks and deduplicates occurrences. Go applies certified audio
// timing and the local renderer/template policy.
func ResolveEntityOverlayPlanWithRanker(ctx context.Context, timeline EntityTimeline, planID, videoID, projectID string, width, height, fpsNum, fpsDen int, ranker EntityOverlayRanker) (capmodel.OverlayPlan, error) {
	if err := timeline.Validate(); err != nil {
		return capmodel.OverlayPlan{}, err
	}
	if ranker == nil {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust ranker is required")
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: plan_id and video_id are required")
	}
	if width <= 0 || height <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: width, height and frame rate must be positive")
	}
	return resolveEntityOverlayPlan(ctx, timeline, planID, videoID, projectID, width, height, fpsNum, fpsDen, ranker)
}

// resolveEntityOverlayPlan applies BrainRust's stable occurrence order to the
// canonical render DTO while retaining Go-owned timing and decoration.
func resolveEntityOverlayPlan(ctx context.Context, timeline EntityTimeline, planID, videoID, projectID string, width, height, fpsNum, fpsDen int, ranker EntityOverlayRanker) (capmodel.OverlayPlan, error) {
	rankingScenes := make([]EntityOverlayRankingScene, len(timeline.Scenes))
	for sceneIndex, scene := range timeline.Scenes {
		rankingScenes[sceneIndex].Entities = make([]EntityOverlayRankingOccurrence, 0, len(scene.Entities))
		for _, occurrence := range scene.Entities {
			rankingScenes[sceneIndex].Entities = append(rankingScenes[sceneIndex].Entities, EntityOverlayRankingOccurrence{
				EntityID: occurrence.EntityID, OverlayID: overlayItemID(occurrence),
				EntityType: NormalizeType(occurrence.Type), Confidence: occurrence.Confidence,
			})
		}
	}
	rankedIndices, err := ranker.RankEntityOverlays(ctx, rankingScenes)
	if err != nil {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust ranker: %w", err)
	}
	if len(rankedIndices) != len(timeline.Scenes) {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust returned %d scene rankings for %d scenes", len(rankedIndices), len(timeline.Scenes))
	}

	var items []capmodel.OverlayItem
	for sceneIndex, scene := range timeline.Scenes {
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
		seenIndices := make(map[int]struct{}, len(rankedIndices[sceneIndex]))
		seenOverlayIdentities := make(map[string]struct{}, len(rankedIndices[sceneIndex]))
		for _, occurrenceIndex := range rankedIndices[sceneIndex] {
			if occurrenceIndex < 0 || occurrenceIndex >= len(scene.Entities) {
				return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust returned occurrence index %d outside scene %q", occurrenceIndex, scene.SceneID)
			}
			if _, duplicate := seenIndices[occurrenceIndex]; duplicate {
				return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust repeated occurrence index %d in scene %q", occurrenceIndex, scene.SceneID)
			}
			seenIndices[occurrenceIndex] = struct{}{}
			occurrence := scene.Entities[occurrenceIndex]
			identity := overlayItemID(occurrence)
			if _, duplicate := seenOverlayIdentities[identity]; duplicate {
				return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust repeated overlay identity %q in scene %q", identity, scene.SceneID)
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
			// A short mention near a scene boundary must not bleed the card into
			// the next scene. The readability floor still applies whenever the
			// scene has room; otherwise the certified scene end wins.
			if sceneEndUS := entitySceneEndUS(timeline, sceneIndex); sceneEndUS > startUS {
				if remaining := sceneEndUS - startUS; durationUS > remaining {
					durationUS = remaining
				}
			}
			kind := capmodel.EntityTypeToKind(occurrence.Type)
			entry, err := capmodel.DefaultChrononOverlayRegistry.Resolve(string(kind))
			if err != nil {
				return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
			}
			items = append(items, capmodel.OverlayItem{
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
				EntityRef: &capmodel.OverlayEntityRef{
					EntityID:    occurrence.EntityID,
					Type:        occurrence.Type,
					Name:        occurrence.Name,
					SurfaceText: occurrence.Name,
				},
			})
		}
		expected := make(map[string]struct{}, len(scene.Entities))
		for _, occurrence := range scene.Entities {
			expected[overlayItemID(occurrence)] = struct{}{}
		}
		if len(seenOverlayIdentities) != len(expected) {
			return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: BrainRust selected %d overlay identities for %d unique identities in scene %q", len(seenOverlayIdentities), len(expected), scene.SceneID)
		}
	}
	if len(items) == 0 {
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: timeline carries no entity occurrences")
	}

	plan := capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
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
		return capmodel.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
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

// entitySceneEndUS returns the certified end of one timeline scene. The
// entity projection does not carry a per-scene duration, so the next scene's
// start (or the run duration for the final scene) is the canonical boundary.
func entitySceneEndUS(timeline EntityTimeline, sceneIndex int) int64 {
	if sceneIndex+1 < len(timeline.Scenes) {
		return timeline.Scenes[sceneIndex+1].TimelineStartUS
	}
	return timeline.DurationUS
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

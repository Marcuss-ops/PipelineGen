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
	"regexp"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// validatePlannedEditingSpans rejects invalid timing before either expensive
// sibling starts. It uses the canonical timeline, never a synthetic audio asset.
func validatePlannedEditingSpans(result *contract.GenerateResult) error {
	if result == nil || result.CanonicalTimeline == nil {
		return nil
	}
	t := contract.EditingTimelineV1{DurationUS: result.CanonicalTimeline.DurationUS}
	if result.OverlayPlan != nil && result.OverlayPlan.DurationMS*1000 > t.DurationUS {
		t.DurationUS = result.OverlayPlan.DurationMS * 1000
	}
	for _, segment := range result.CanonicalTimeline.Segments {
		t.Scenes = append(t.Scenes, contract.EditingSceneSpan{SceneID: segment.ID, StartUS: segment.TimelineStartUS, EndUS: segment.TimelineStartUS + segment.DurationUS})
	}
	if result.OverlayPlan != nil {
		t.Overlays = overlaysFromPlan(result)
	}
	if err := t.ValidateSpans(); err != nil {
		return err
	}
	for language, plan := range result.LocalizedOverlayPlans {
		if plan == nil {
			continue
		}
		localized := *result
		localized.OverlayPlan = plan
		t.DurationUS = plan.DurationMS * 1000
		t.Scenes = nil
		t.Overlays = overlaysFromPlan(&localized)
		if err := t.ValidateSpans(); err != nil {
			return fmt.Errorf("localized %s: %w", language, err)
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
func BuildEditingTimeline(result *contract.GenerateResult) (*contract.EditingTimelineV1, error) {
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
	scenes := make([]contract.EditingSceneSpan, 0, len(tl.Segments))
	for _, seg := range tl.Segments {
		scenes = append(scenes, contract.EditingSceneSpan{
			SceneID: seg.ID,
			StartUS: seg.TimelineStartUS,
			EndUS:   seg.TimelineStartUS + seg.DurationUS,
		})
	}

	// Build overlay spans from the overlay plan (when available).
	var overlays []contract.EditingOverlaySpan
	if result.OverlayPlan != nil {
		overlays = overlaysFromPlan(result)
	}

	durationUS := tl.DurationUS
	for _, overlay := range overlays {
		if overlay.EndUS > durationUS {
			durationUS = overlay.EndUS
		}
	}
	et := &contract.EditingTimelineV1{
		Version:    contract.EditingTimelineVersion,
		Timebase:   contract.EditingTimebase,
		DurationUS: durationUS,
		Audio: contract.EditingAudioRef{
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
func overlaysFromPlan(result *contract.GenerateResult) []contract.EditingOverlaySpan {
	plan := result.OverlayPlan
	if plan == nil {
		return nil
	}

	// The rendered artifact is a single certified queue artifact for the
	// whole plan. Its Drive publication identity (DriveLink/SHA256) is the
	// immutable reference every overlay span carries; empty when the render
	// has not completed or was not published yet. The render job id, plan
	// fingerprint and source video id travel with it so the final-video
	// assembly can prove which queue job and plan version produced the
	// overlay it composites.
	var driveLink, sha256, renderJobID string
	if result.OverlayRender != nil {
		renderJobID = result.OverlayRender.JobID
		if result.OverlayRender.Artifact != nil {
			driveLink = result.OverlayRender.Artifact.DriveLink
			sha256 = result.OverlayRender.Artifact.SHA256
		}
	}

	// Build entity timeline lookup for microsecond-precision timing. The key
	// MUST be scene-scoped: the extractor reuses one occurrence id for the same
	// entity across scenes ("Mike Tyson" appears as ent_… in scene-01 and in
	// scene-05), so an id-only map makes the LAST occurrence overwrite every
	// earlier one and moves an early card to the end of the video. That defect
	// put the scene-01 cards at 5:30/6:15 instead of 1.5s/46.6s in r133.
	occBySceneEntity := map[string]struct {
		startUS int64
		endUS   int64
	}{}
	if result.EntityTimeline != nil {
		for _, scene := range result.EntityTimeline.Scenes {
			for _, occ := range scene.Entities {
				sceneID := strings.TrimSpace(scene.SceneID)
				if sceneID == "" {
					sceneID = strings.TrimSpace(occ.SceneID)
				}
				occBySceneEntity[entitySpanKey(sceneID, occ.EntityID)] = struct {
					startUS int64
					endUS   int64
				}{occ.AudioStartUS, occ.AudioEndUS}
			}
		}
	}

	intentByKey := overlayIntentIndex(result.OverlayIntents)

	overlays := make([]contract.EditingOverlaySpan, 0, len(plan.Items))
	for _, item := range plan.Items {
		itemDriveLink, itemSHA256, itemRenderJobID := driveLink, sha256, renderJobID
		if artifact, jobID, ok := overlayArtifactForItem(result.OverlayRender, item.ID); ok {
			itemRenderJobID = jobID
			itemDriveLink = artifact.DriveLink
			itemSHA256 = artifact.SHA256
		}
		// Canonical integer-microsecond timing travels on the item; fall back
		// to the millisecond projection for legacy (golden) plans.
		startUS := item.StartUSValue()
		endUS := item.EndUSValue()

		// Entity card overlays: prefer EntityTimeline microsecond timing from
		// the occurrence that belongs to THIS item's scene.
		if item.EntityID != "" {
			if occ, ok := occBySceneEntity[entitySpanKey(item.SceneID, item.EntityID)]; ok {
				startUS = occ.startUS
				endUS = occ.endUS
			}
		}

		// Trace the plan item back to the pre-timing OverlayIntent it
		// materialized from (the overlay_intent_id hop). The join key is the
		// same (scene, canonical name) the intent and item share.
		itemName := item.Text
		if item.EntityRef != nil {
			if n := strings.TrimSpace(item.EntityRef.Name); n != "" {
				itemName = n
			}
		}
		intentID, intentFingerprint := "", ""
		if intent, ok := lookupOverlayIntent(intentByKey, item.SceneID, item.EntityID, itemName); ok {
			intentID = intent.IntentID
			intentFingerprint = intent.Fingerprint()
		}

		overlays = append(overlays, contract.EditingOverlaySpan{
			ArtifactID:         item.ID,
			SceneID:            item.SceneID,
			Entity:             item.Text,
			TemplateID:         item.TemplateID,
			StartUS:            startUS,
			EndUS:              endUS,
			DriveLink:          itemDriveLink,
			SHA256:             itemSHA256,
			MediaContract:      plan.MediaContract,
			RenderJobID:        itemRenderJobID,
			PlanFingerprint:    plan.Fingerprint,
			RenderKey:          item.RenderKey,
			SourceVideoAssetID: plan.VideoID,
			IntentID:           intentID,
			IntentFingerprint:  intentFingerprint,
		})
	}
	return overlays
}

// entitySpanKey is the scene-scoped correlation key between a plan item and
// its entity occurrence. Occurrence ids are only unique inside a scene, so the
// scene id is part of the key.
func entitySpanKey(sceneID, entityID string) string {
	return strings.ToLower(strings.TrimSpace(sceneID)) + "\x00" + strings.TrimSpace(entityID)
}

// overlayIntentKey builds the lookup key that joins a plan item to its
// pre-timing OverlayIntent: the normalized (scene id, entity name) pair both
// surfaces derive from. It is a correlation key, not a display string.
func overlayIntentKey(sceneID, name string) string {
	return strings.ToLower(strings.TrimSpace(sceneID)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
}

// overlayIntentIDKey is the stable-identity correlation key. The "id:" prefix
// namespaces it so it can never collide with a display-name key.
func overlayIntentIDKey(sceneID, stableEntityID string) string {
	return overlayIntentKey(sceneID, "id:"+strings.TrimSpace(stableEntityID))
}

// overlayIntentIndex builds a lookup from the run's pre-timing OverlayIntents,
// so an overlay plan item can be traced back to the exact intent it
// materialized from. An entity intent is indexed under its content-addressed
// stable entity id — the identity the plan item carries — so the join never
// depends on the two surfaces spelling a name identically. The normalized
// (scene id + canonical name) key is still indexed for annotation-sourced
// (phrase/word) intents and for legacy plans whose items carry no entity id.
func overlayIntentIndex(intents []capmodel.OverlayIntent) map[string]capmodel.OverlayIntent {
	index := make(map[string]capmodel.OverlayIntent, len(intents))
	for _, intent := range intents {
		name := strings.TrimSpace(intent.Entity.CanonicalName)
		if name == "" {
			name = strings.TrimSpace(intent.SourceText)
		}
		if name == "" {
			continue
		}
		index[overlayIntentKey(intent.SceneID, name)] = intent
		if intent.Source == capmodel.IntentSourceEntity {
			if stable := capabilityentities.StableEntityID(intent.Entity.Type, intent.Entity.CanonicalName); stable != "" {
				index[overlayIntentIDKey(intent.SceneID, stable)] = intent
			}
		}
	}
	return index
}

// lookupOverlayIntent resolves the pre-timing OverlayIntent a plan item
// materialized from. The stable entity id is tried first (exact, spelling
// independent); the normalized (scene, canonical name) pair is the documented
// fallback for legacy items without an entity id and for annotation intents.
func lookupOverlayIntent(index map[string]capmodel.OverlayIntent, sceneID, stableEntityID, name string) (capmodel.OverlayIntent, bool) {
	if strings.TrimSpace(stableEntityID) != "" {
		if intent, ok := index[overlayIntentIDKey(sceneID, stableEntityID)]; ok {
			return intent, true
		}
	}
	intent, ok := index[overlayIntentKey(sceneID, name)]
	return intent, ok
}

// OverlayArtifactRef is the reference to a rendered overlay artifact
// that gets included in the editing timeline. It carries the identity
// and timing; the location lives in the canonical asset_locations row.
type OverlayArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	SceneID    string `json:"scene_id"`
	TemplateID string `json:"template_id"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	SHA256     string `json:"sha256"`
	// MediaContract is the contract ID this artifact was validated against.
	MediaContract string `json:"media_contract"`
}

// EditingOverlayPlanItem returns an OverlayItem-compatible projection of
// an editing overlay span, suitable for consumption by downstream editing
// systems that need the OverlayItem shape with microsecond timing.

// planOverlayIntentsForAnnotations plans overlay intents from the read-only
// snapshot + the per-scene computed annotations (keyed by scene index). It is
// the pure counterpart of planOverlayIntents used by the concurrent
// overlay.prepare branch, which never reads the mutable Scene struct.
func planOverlayIntentsForAnnotations(snapshot []sceneTextSnapshot, annotations map[int]*scriptpkg.SceneAnnotations, registry *capmodel.ChrononOverlayRegistry) []capmodel.OverlayIntent {
	var inputs []capmodel.SceneEntityInput
	for i, s := range snapshot {
		if input, ok := sceneEntityInput(s.ID, i, annotations[i]); ok {
			inputs = append(inputs, input)
		}
	}
	return capmodel.PlanOverlayIntents(inputs, registry)
}

// entityIntentExcludedTypes are the annotation families that never author an
// entity intent. DATE/TIME/NUMBER/MONEY/PERCENT belong to the planner's
// numeric lane: admitting them here only creates unresolved intents that can
// never become an entity card (TICKET-49).
var entityIntentExcludedTypes = map[string]struct{}{
	entitytype.Date:    {},
	entitytype.Time:    {},
	entitytype.Number:  {},
	entitytype.Money:   {},
	entitytype.Percent: {},
}

// slugUUIDPattern matches the UUID tail of a URL/slug. An extracted "entity"
// that carries one is a tokenization artifact, not a canonical identity.
var slugUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// entityIntentEligible is the admission gate for one annotation entity. It
// keeps the numeric families in their own lane and rejects slug/UUID
// fragments; every other type keeps the existing type -> template contract.
func entityIntentEligible(entity scriptpkg.AnnotatedEntity) bool {
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		return false
	}
	if _, excluded := entityIntentExcludedTypes[entitytype.Fold(entity.Type)]; excluded {
		return false
	}
	return !slugUUIDPattern.MatchString(name)
}

// sceneEntityInput projects one scene's grounded annotations onto the neutral
// input types the overlay planner consumes. It returns false when the scene
// has no annotations (or no derivable entities/phrases/words), so the caller
// can skip it.
func sceneEntityInput(sceneID string, sceneIndex int, ann *scriptpkg.SceneAnnotations) (capmodel.SceneEntityInput, bool) {
	if ann == nil {
		return capmodel.SceneEntityInput{}, false
	}
	var entities []capmodel.EntityOverlayInput
	appendEntity := func(entity scriptpkg.AnnotatedEntity) {
		if !entityIntentEligible(entity) {
			return
		}
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			return
		}
		var image *capmodel.EntityImageOverlayInput
		if entity.Image != nil && strings.TrimSpace(entity.Image.AssetID) != "" && strings.TrimSpace(entity.Image.SHA256) != "" {
			image = &capmodel.EntityImageOverlayInput{
				EntityName: name,
				AssetID:    entity.Image.AssetID,
				URL:        entityImageURL(entity.Image),
				SHA256:     entity.Image.SHA256,
			}
		}
		entities = append(entities, capmodel.EntityOverlayInput{
			Name:       name,
			Type:       strings.TrimSpace(entity.Type),
			Confidence: entity.Confidence,
			Image:      image,
			// The canonical identity travels WITH the annotation: the intent
			// carries it verbatim so nothing downstream has to re-derive (or
			// re-guess) which entity an overlay is about.
			CanonicalID: annotationCanonicalEntityID(entity),
		})
	}
	for _, entity := range ann.PrimaryEntities {
		appendEntity(entity)
	}
	for _, entity := range ann.SecondaryEntities {
		appendEntity(entity)
	}
	var phrases []capmodel.OverlayAnnotationInput
	for _, phrase := range ann.ImportantPhrases {
		phrases = append(phrases, capmodel.OverlayAnnotationInput{ID: phrase.ID, Text: phrase.Text})
	}
	var words []capmodel.OverlayAnnotationInput
	for _, word := range ann.ImportantWords {
		words = append(words, capmodel.OverlayAnnotationInput{ID: word.ID, Text: word.Text})
	}
	return capmodel.SceneEntityInput{
		SceneID:    sceneID,
		SceneIndex: sceneIndex,
		Entities:   entities,
		Phrases:    phrases,
		Words:      words,
	}, true
}

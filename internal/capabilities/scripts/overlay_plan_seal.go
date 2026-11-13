// overlay_plan_seal.go — the plan-sealing boundary of overlay compilation:
// render-extent derivation (audio-plan authoritative + scene/item spans),
// provisional-key reset, OverlayPlan assembly and Validate.
//
// Split from overlay_plan_compile.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package scriptgeneration

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	"go.uber.org/zap"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// sealOverlayPlan derives the master render extent, resets provisional
// render keys after post-processing and assembles + validates the final
// OverlayPlan. Split from compileOverlayPlanWithMotionOffset (same behavior).
func sealOverlayPlan(result *GenerateResult, language Language, plannerPlan capmodel.OverlayPlan, resolved []ResolvedScene, items []capmodel.OverlayItem, canvas OverlayCanvasSpec, planID, videoID, projectID string) (*capmodel.OverlayPlan, error) {
	// Maps are composited in replace mode, so any shared frame makes the final
	// plan invalid. Resolve those collisions while scene timing is still in
	// scope: shift ordinary cards after the map within their scene, and drop
	// only cards that cannot fit. Anchored editorial timing remains untouched.
	sceneWindows := make(map[string][2]int64, len(resolved))
	for _, scene := range resolved {
		if scene.ID == "" || scene.DurationUS <= 0 {
			continue
		}
		sceneWindows[scene.ID] = [2]int64{scene.TimelineStartUS, scene.TimelineStartUS + scene.DurationUS}
	}
	items = capabilityoverlay.ResolveMapOverlaps(items, sceneWindows, canvas.FPSNum, canvas.FPSDen)
	// Preserve the scene extent as a defensive lower bound when a malformed or
	// legacy result has no final-audio reference.
	var durationUS int64
	if result.SourceLanguage == "" || result.SourceLanguage == language {
		if result.AudioPlan != nil {
			// A sealed plan exists before encoding. Its master extent, not
			// encoder packet padding, freezes identical serial/parallel plans.
			durationUS = result.AudioPlan.DurationUS
			if result.AudioPlan.MasterDurationUS > durationUS {
				durationUS = result.AudioPlan.MasterDurationUS
			}
		} else if result.FinalAudio != nil && result.FinalAudio.DurationMS > 0 {
			durationUS = result.FinalAudio.DurationMS * 1000
		}
	}
	for _, scene := range resolved {
		if scene.TimelineStartUS < 0 || scene.DurationUS <= 0 {
			continue
		}
		if endUS := scene.TimelineStartUS + scene.DurationUS; endUS > durationUS {
			durationUS = endUS
		}
	}
	// Date cards intentionally outlive their spoken word timing. Their render
	// plan canvas must include the full item interval; this affects video/overlay
	// duration only and does not pad or rewrite the canonical audio plan.
	for _, item := range items {
		if endUS := item.EndUSValue(); endUS > durationUS {
			durationUS = endUS
		}
	}
	durationMS := int64(0)
	if durationUS > 0 {
		// DurationMS is a transport projection; round up so a sub-ms tail is
		// never lost when it crosses the PipelineGen → Chronon boundary.
		durationMS = (durationUS + 999) / 1000
	}
	// Item transforms above (chain coalescing, font/style projection and
	// image composition) can change renderer-owned inputs after an upstream
	// planner has sealed its own intermediate plan. This is the final plan
	// sealing boundary, so discard those provisional keys and let Validate
	// recompute them from the actual serialized render inputs.
	for i := range items {
		items[i].RenderKey = ""
	}

	plan := capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     strings.TrimSpace(projectID),
		// Forward the explicit render seed so the worker resamples its own
		// "random" entity styles per render; empty keeps historical
		// plan-identity sampling and byte-identical wire plans.
		EntityVariationSeed:    entityVariationSeedForCanvas(canvas),
		ScriptName:             textutil.FirstNonEmpty(result.OutputName, result.Title, projectID),
		Language:               string(language),
		Width:                  canvas.Width,
		Height:                 canvas.Height,
		FPSNum:                 canvas.FPSNum,
		FPSDen:                 canvas.FPSDen,
		DurationMS:             durationMS,
		ForegroundScalePercent: canvas.ForegroundScalePercent,
		Background:             canvas.Background,
		Items:                  items,
		// Carry the planner's recap observability into the sealed plan:
		// plannerPlan itself is superseded here by post-processing (entity
		// overlays, budgets, chain fusion). RecapCards is recounted on the
		// final items so fused-away markers cannot inflate it.
		RecapPlacements: plannerPlan.RecapPlacements,
		RecapCards:      countRecapItems(items),
		ChartPlacements: plannerPlan.ChartPlacements,
		ChartCards:      countChartItems(items),
		TimedScenes:     plannerPlan.TimedScenes,
	}
	// The output identity is stamped in ONE place: the certification profile the
	// renderer will be held to, and the media contract derived from it. The
	// contract travels with the plan (never re-derived downstream) and the
	// compiled chronon output derives container/codec/pixel format from it, so
	// the sealed plan carries one statement about the artifact instead of two
	// that can disagree. The stamp runs BEFORE Validate because the plan
	// fingerprint is computed from its contents.
	plan.StampOutputProfile(canvas.OutputProfileID)
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// entityVariationSeedForCanvas forwards an explicit per-render motion seed to
// the worker's entity-style sampler. Empty preserves the historical
// plan-identity sampling so existing sealed plans stay byte-identical. It
// lives beside the seal because the seed is frozen into the sealed plan.
func entityVariationSeedForCanvas(canvas OverlayCanvasSpec) string {
	return strings.TrimSpace(canvas.MotionSeed)
}

// attachEntityCardAsset resolves the card's canonical_entity_id through the
// EntityMediaResolver and promotes the item to the image-only entity
// capability when a verified asset exists. The name remains available in the
// identity/provenance fields, but it is no longer rendered as a second lower
// third below the portrait. An entity without an indexed asset is returned
// unchanged (text-only card).
func attachEntityCardAsset(item capmodel.OverlayItem, media *capabilityentities.EntityMediaResolver, canonicalByStable map[string]string, planID string) capmodel.OverlayItem {
	if media == nil {
		logEntityCardMediaMiss(item, "MEDIA_INDEX_MISS", "resolver is nil", "")
		return item
	}
	lookupIDs := entityCardLookupIDs(item, canonicalByStable)
	var lastErr error
	var lastLookup string
	for _, lookupID := range lookupIDs {
		ref, err := media.ResolveBest(lookupID)
		if err != nil {
			lastErr, lastLookup = err, lookupID
			continue
		}
		// A catalog row without a fetchable URL is not renderable by Chronon.
		// Keep the entity card text-only until the asset binding is complete;
		// emitting a hash-only ref would make RenderingGen fail later while
		// materializing an impossible logical path.
		if strings.TrimSpace(ref.URL) == "" || strings.TrimSpace(ref.SHA256) == "" || strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/semantic/") || strings.HasPrefix(strings.TrimSpace(ref.URL), "semantic/") {
			lastErr, lastLookup = fmt.Errorf("resolved asset has no renderable URL or SHA-256"), lookupID
			continue
		}
		return promoteEntityCardToImage(item, ref, lookupID, planID)
	}
	if lastErr != nil {
		logEntityCardMediaMiss(item, "ASSET_UNMATERIALIZED", lastErr.Error(), lastLookup)
		return item
	}
	logEntityCardMediaMiss(item, "MEDIA_INDEX_MISS", "no canonical identity was indexed", "")
	return item
}

// entityCardLookupIDs tries every identity projection the resolver card can
// carry. The plan joins on the item ID in the common case, but a chain or a
// localized resolver may preserve only the stable entity id, the producer's
// EntityRef id, or the canonical id. Trying them in order makes the join
// identity-based instead of depending on one serialization format.
func entityCardLookupIDs(item capmodel.OverlayItem, canonicalByStable map[string]string) []string {
	keys := []string{item.ID, item.EntityID}
	if item.EntityRef != nil {
		keys = append(keys, item.EntityRef.EntityID, item.EntityRef.CanonicalEntityID)
	}
	out := make([]string, 0, len(keys)+4)
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		for _, existing := range out {
			if existing == id {
				return
			}
		}
		out = append(out, id)
	}
	for _, key := range keys {
		appendID(canonicalByStable[key])
	}
	for _, key := range keys {
		appendID(key)
	}
	if ref := item.EntityRef; ref != nil {
		if sceneID := strings.TrimSpace(item.SceneID); sceneID != "" && strings.TrimSpace(ref.CanonicalEntityID) != "" {
			appendID(sceneID + "::" + strings.TrimSpace(ref.CanonicalEntityID))
		}
		if ref.Type != "" && ref.Name != "" {
			stable := capabilityentities.StableEntityID(ref.Type, ref.Name)
			appendID(canonicalByStable[stable])
			appendID(stable)
			if sceneID := strings.TrimSpace(item.SceneID); sceneID != "" {
				appendID(sceneID + "::" + stable)
			}
		}
	}
	// Last identity projection: the verified bundle indexed this display name
	// even when the resolver card carries neither a canonical id nor an
	// occurrence id (generated agency/location cards).
	name := strings.TrimSpace(item.EntityCaption)
	if name == "" {
		name = strings.TrimSpace(item.Text)
	}
	if key := normalizeEntityAssetKey(name); key != "" {
		appendID(canonicalByStable["bundle-name:"+key])
	}
	return out
}

func promoteEntityCardToImage(item capmodel.OverlayItem, ref capabilityentities.ResolvedAssetRef, lookupID, planID string) capmodel.OverlayItem {
	item.AssetRefs = []capmodel.OverlayAssetRef{{
		AssetID: ref.SHA256, URL: ref.URL, LocalPath: ref.LocalPath, SHA256: ref.SHA256, MediaType: ref.MediaType,
	}}
	// A resolved portrait is a distinct visual capability: use the official
	// image preset/motion path and do not compile the text-card layer as well.
	item.Kind = string(capmodel.KindEntityImage)
	item.TemplateID = "image_popup"
	item.PresetID = capabilityoverlay.SelectEntityImagePreset(planID, item.SceneID, item.ID)
	// Preserve the entity name as a real caption layer. Text is cleared below
	// because image items do not render the generic text field.
	item.EntityCaption = entityImageCaption(item)
	// Keep composition selection opt-in. The planner assigns a GPU-safe image
	// reveal and caption motion; runtime renders must not choose a random
	// spatial composition.
	item.EntityStyleID = ""
	// The run-level pass assigns a non-repeating catalog motion after image
	// overlays have been capped.
	item.MotionID = ""
	item.MotionParams = nil
	// RenderingGen requires the image treatment explicitly when an entity
	// image also carries an entity-card composition selector. Keep the
	// animation preset above independent from this image-fit preset.
	item.ImagePresetID = "image_scale_in"
	item.Text = ""
	item.Params = nil
	if item.EntityRef == nil {
		item.EntityRef = &capmodel.OverlayEntityRef{}
	}
	canonicalID := lookupID
	if separator := strings.Index(canonicalID, "::"); separator >= 0 {
		canonicalID = canonicalID[separator+2:]
	}
	item.EntityRef.CanonicalEntityID = canonicalID
	return item
}

func logEntityCardMediaMiss(item capmodel.OverlayItem, reason, detail, lookupID string) {
	entity := ""
	if item.EntityRef != nil {
		entity = item.EntityRef.Name
	}
	if entity == "" {
		entity = item.Text
	}
	logger.Warn("overlay plan: entity card left text-only",
		zap.String("reason", reason),
		zap.String("item_id", item.ID),
		zap.String("entity_id", item.EntityID),
		zap.String("entity", entity),
		zap.String("lookup_id", lookupID),
		zap.String("detail", detail),
	)
}

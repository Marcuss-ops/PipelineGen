// overlay_plan_seal.go — the plan-sealing boundary of overlay compilation:
// render-extent derivation (audio-plan authoritative + scene/item spans),
// provisional-key reset, OverlayPlan assembly and Validate.
//
// Split from overlay_plan_compile.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package scriptgeneration

import (
	"fmt"
	"strings"

	"go.uber.org/zap"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
)

// sealCompiledOverlayPlan adopts a plan produced by the v2 compiler. Rust owns
// selection, timing and map resolution; this boundary owns only the derived
// render identity and the master extent, so the document that reaches the
// bundle hash, audit, renderer and checkpoint is the same sealed document.
func sealCompiledOverlayPlan(plan *capmodel.OverlayPlan, result *GenerateResult, language Language, resolved []ResolvedScene, canvas OverlayCanvasSpec, perScene ...bool) (*capmodel.OverlayPlan, error) {
	if plan == nil {
		return nil, nil
	}
	plan.SchemaVersion = capmodel.SchemaVersionPlan
	plan.Language = string(language)
	return finalizeSealedOverlayPlan(*plan, result, language, resolved, canvas, perScene...)
}

// promoteVerifiedEntityCards upgrades compiled entity cards to the image-only
// entity capability when the run already holds a verified, content-addressed
// portrait for the identity. The v2 compiler plans identity cards from the
// entity timeline but does not own the media index, so before this pass every
// card stayed text-only even though the portrait had been acquired and
// published: the final-job preflight then refused an image-requested render
// with zero image items. The join is identity-based (canonical id first, then
// stable id), which is what lets a portrait verified for the source language
// render unchanged in a translated plan. Explicit images=disabled wins; an
// unverified or missing asset leaves the card text-only, as before.
func promoteVerifiedEntityCards(plan *capmodel.OverlayPlan, result *GenerateResult, canvas OverlayCanvasSpec, perScene ...bool) {
	if plan == nil || result == nil || len(plan.Items) == 0 {
		return
	}
	if toggle, ok := canvas.OverlayCategories[scriptpkg.OverlayCategoryImages]; ok && toggle == scriptpkg.ToggleDisabled {
		return
	}
	if !hasPendingEntityCard(plan.Items) {
		return
	}
	media, canonicalByStable := entityCardMediaIndex(result, perScene...)
	promoted := 0
	for i := range plan.Items {
		item := plan.Items[i]
		if _, isChain := capmodel.ChainSegmentMaps(item.Params); isChain {
			upgradedChain, chainPromoted := promoteChainEntityCardSegments(item, media, canonicalByStable, plan.PlanID)
			if chainPromoted > 0 {
				plan.Items[i] = upgradedChain
				promoted += chainPromoted
			}
			continue
		}
		if !entityCardKind(capmodel.OverlayKind(item.Kind)) || len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0 {
			continue
		}
		upgraded := attachEntityCardAsset(item, media, canonicalByStable, plan.PlanID)
		if upgraded.Kind != string(capmodel.KindEntityImage) {
			continue
		}
		plan.Items[i] = upgraded
		promoted++
	}
	if promoted > 0 {
		plan.Items = capEntityImageOverlays(plan.Items, capabilityoverlay.MaxEntityImageOverlaysPerRun, perScene...)
	}
}

func hasPendingEntityCard(items []capmodel.OverlayItem) bool {
	for _, item := range items {
		if segments, isChain := capmodel.ChainSegmentMaps(item.Params); isChain {
			for _, seg := range segments {
				kind, _ := seg["kind"].(string)
				if entityCardKind(capmodel.OverlayKind(kind)) && !chainSegmentHasAssets(seg) {
					return true
				}
			}
			continue
		}
		if entityCardKind(capmodel.OverlayKind(item.Kind)) && len(item.AssetRefs) == 0 && len(item.ImageLayers) == 0 {
			return true
		}
	}
	return false
}

func chainSegmentHasAssets(seg map[string]any) bool {
	if assetID, _ := seg["asset_id"].(string); strings.TrimSpace(assetID) != "" {
		return true
	}
	if assets, ok := seg["assets"].([]capmodel.OverlayAssetRef); ok && len(assets) > 0 {
		return true
	}
	if assets, ok := seg["assets"].([]any); ok && len(assets) > 0 {
		return true
	}
	if layers, ok := seg["image_layers"].([]capmodel.OverlayImageLayer); ok && len(layers) > 0 {
		return true
	}
	if layers, ok := seg["image_layers"].([]any); ok && len(layers) > 0 {
		return true
	}
	return false
}

func promoteChainEntityCardSegments(item capmodel.OverlayItem, media *capabilityentities.EntityMediaResolver, canonicalByStable map[string]string, planID string) (capmodel.OverlayItem, int) {
	segments, ok := capmodel.ChainSegmentMaps(item.Params)
	if !ok || len(segments) == 0 {
		return item, 0
	}
	promoted := 0
	allImages := true
	seenAssets := make(map[string]struct{}, len(item.AssetRefs))
	for _, ref := range item.AssetRefs {
		if ref.AssetID != "" {
			seenAssets[ref.AssetID] = struct{}{}
		}
	}
	for idx, seg := range segments {
		kind, _ := seg["kind"].(string)
		if !entityCardKind(capmodel.OverlayKind(kind)) || chainSegmentHasAssets(seg) {
			if kind != string(capmodel.KindEntityImage) && kind != string(capmodel.KindImagePopup) {
				allImages = false
			}
			continue
		}
		synthetic := chainSegmentOverlayItem(item, seg)
		upgraded := attachEntityCardAsset(synthetic, media, canonicalByStable, planID)
		if upgraded.Kind != string(capmodel.KindEntityImage) || len(upgraded.AssetRefs) == 0 {
			allImages = false
			continue
		}
		seg["kind"] = string(capmodel.KindEntityImage)
		seg["template_id"] = "image_popup"
		seg["preset_id"] = upgraded.PresetID
		seg["image_preset_id"] = upgraded.ImagePresetID
		seg["entity_caption"] = upgraded.EntityCaption
		seg["asset_id"] = upgraded.AssetRefs[0].AssetID
		seg["assets"] = upgraded.AssetRefs
		delete(seg, "text")
		delete(seg, "entity_style_id")
		delete(seg, "motion_id")
		delete(seg, "motion_params")
		delete(seg, "params")
		for _, ref := range upgraded.AssetRefs {
			if _, exists := seenAssets[ref.AssetID]; !exists {
				seenAssets[ref.AssetID] = struct{}{}
				item.AssetRefs = append(item.AssetRefs, ref)
			}
		}
		if idx == 0 {
			item.PresetID = upgraded.PresetID
			item.Text = ""
		}
		promoted++
	}
	if promoted == 0 {
		return item, 0
	}
	item.Params["chain_segments"] = segments
	if allImages {
		item.Kind = capmodel.KindImageChain
		item.TemplateID = capmodel.TemplateImageChain
	}
	item.RenderKey = ""
	return item, promoted
}

func chainSegmentOverlayItem(parent capmodel.OverlayItem, seg map[string]any) capmodel.OverlayItem {
	kind, _ := seg["kind"].(string)
	text, _ := seg["text"].(string)
	entityID, _ := seg["entity_id"].(string)
	entityCaption, _ := seg["entity_caption"].(string)
	out := capmodel.OverlayItem{
		ID:            parent.ID,
		SceneID:       parent.SceneID,
		Kind:          kind,
		Text:          text,
		EntityID:      entityID,
		EntityCaption: entityCaption,
		EntityRef:     parent.EntityRef,
	}
	if out.EntityID == "" {
		out.EntityID = parent.EntityID
	}
	return out
}

// finalizeSealedOverlayPlan derives the master extent, resets provisional render
// keys and stamps the one output identity before Validate computes the final
// fingerprint. It is the only place a plan becomes wire-eligible.
func finalizeSealedOverlayPlan(plan capmodel.OverlayPlan, result *GenerateResult, language Language, resolved []ResolvedScene, canvas OverlayCanvasSpec, perScene ...bool) (*capmodel.OverlayPlan, error) {
	// Identity media promotion runs before any derived field: the promoted item
	// changes kind, template and asset refs, so the extent and fingerprint must
	// be computed from the document the renderer will actually receive.
	promoteVerifiedEntityCards(&plan, result, canvas, perScene...)
	// Explicit payload typography is projected after the planner's presentation
	// defaults: the run-level style styles every text lane, while the dedicated
	// overlay_style.bullet block styles bullet-point copy only and falls back to
	// the run-level choice field by field. Fields the payload left unset keep the
	// planner default.
	applyPayloadOverlayStyleToItems(plan.Items, canvas)
	plan.DurationMS = overlayPlanDurationMS(result, language, resolved, plan.Items)
	// Item transforms above (chain coalescing, font/style projection and image
	// composition) can change renderer-owned inputs after an upstream planner
	// has sealed its own intermediate plan. This is the final plan sealing
	// boundary, so discard those provisional keys and let Validate recompute
	// them from the actual serialized render inputs.
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	plan.RecapCards = countRecapItems(plan.Items)
	plan.ChartCards = countChartItems(plan.Items)
	plan.Fingerprint = ""
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

// overlayPlanDurationMS derives the master render extent from the authoritative
// audio plan, with the scene and item tails as lower bounds.
func overlayPlanDurationMS(result *GenerateResult, language Language, resolved []ResolvedScene, items []capmodel.OverlayItem) int64 {
	if result == nil {
		return 0
	}
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
	if durationUS <= 0 {
		return 0
	}
	// DurationMS is a transport projection; round up so a sub-ms tail is
	// never lost when it crosses the PipelineGen → Chronon boundary.
	return (durationUS + 999) / 1000
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
	// This key also carries the single-token family-name aliases the media
	// index publishes for unique verified person identities: a translated
	// card that keeps only "Tyson" for "Mike Tyson" still resolves here.
	if key := entitycatalog.NormalizeVerifiedAssetKey(name); key != "" {
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

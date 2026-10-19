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

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// sealOverlayPlan derives the master render extent, resets provisional
// render keys after post-processing and assembles + validates the final
// OverlayPlan. Split from compileOverlayPlanWithMotionOffset (same behavior).
func sealOverlayPlan(result *GenerateResult, language Language, plannerPlan capabilityoverlay.OverlayPlan, resolved []ResolvedScene, items []capabilityoverlay.OverlayItem, canvas OverlayCanvasSpec, planID, videoID, projectID string) (*capabilityoverlay.OverlayPlan, error) {
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

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
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
		AnimationPolicies:      scriptoverlay.ToOverlayAnimationPolicies(canvas.AnimationPolicies),
		// Overlays are composited over the master video, so they require an
		// alpha channel. The contract travels with the plan (never re-derived
		// downstream); the compiled chronon output derives container/codec/
		// pixel format from it.
		MediaContract: capabilityoverlay.ContractIDForCanvas(canvas.Width, canvas.Height, canvas.FPSNum, canvas.FPSDen, true),
		Items:         items,
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
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("overlay plan: seal: %w", err)
	}
	return &plan, nil
}

// attachEntityCardAsset resolves the card's canonical_entity_id through the
// EntityMediaResolver and promotes the item to the image-only entity
// capability when a verified asset exists. The name remains available in the
// identity/provenance fields, but it is no longer rendered as a second lower
// third below the portrait. An entity without an indexed asset is returned
// unchanged (text-only card).
func attachEntityCardAsset(item capabilityoverlay.OverlayItem, media *capabilityentities.EntityMediaResolver, canonicalByStable map[string]string, planID string) capabilityoverlay.OverlayItem {
	lookupID := canonicalByStable[item.ID]
	if lookupID == "" {
		lookupID = canonicalByStable[item.EntityID]
	}
	if lookupID == "" {
		return item
	}
	ref, err := media.ResolveBest(lookupID)
	if err != nil {
		return item
	}
	// A catalog row without a fetchable URL is not renderable by Chronon.
	// Keep the entity card text-only until the asset binding is complete;
	// emitting a hash-only ref would make RenderingGen fail later while
	// materializing an impossible logical path.
	if strings.TrimSpace(ref.URL) == "" || strings.TrimSpace(ref.SHA256) == "" || strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/semantic/") || strings.HasPrefix(strings.TrimSpace(ref.URL), "semantic/") {
		return item
	}
	item.AssetRefs = []capabilityoverlay.OverlayAssetRef{{
		AssetID: ref.SHA256, URL: ref.URL, LocalPath: ref.LocalPath, SHA256: ref.SHA256, MediaType: ref.MediaType,
	}}
	// A resolved portrait is a distinct visual capability: use the official
	// image preset/motion path and do not compile the text-card layer as well.
	item.Kind = string(capabilityoverlay.KindEntityImage)
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
		item.EntityRef = &capabilityoverlay.OverlayEntityRef{}
	}
	canonicalID := lookupID
	if separator := strings.Index(canonicalID, "::"); separator >= 0 {
		canonicalID = canonicalID[separator+2:]
	}
	item.EntityRef.CanonicalEntityID = canonicalID
	return item
}

// Package scriptgeneration — overlay_plan_freeze.go owns the seams around the
// sealed OverlayPlan.
//
// First seam, the render-boundary promotion of the pre-timing OverlayIntents:
// the exact join that binds each authoring intent to the plan item it
// materialized from, and the copy of that item's certified facts onto the
// intent.
//
// Second seam, the per-language plan boundary: ONE plan per language that has
// its own translated voiceover timing, compiled by the same SSOT compiler and
// never substituting the source plan for a missing translation.
//
// Both live beside overlay_plan.go (not inside it): the file is at the strict
// LOC cap, and each seam is a boundary rather than a compilation step. The
// intent join is the identity-sensitive half of the lowering — the intent is
// authored before media resolution, so it is matched to the final item by
// IDENTITY, never by the display text two independent surfaces happen to carry
// — while the per-language boundary is the publication-sensitive half.
package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// compileResultOverlayPlan attaches the sealed source/localized plans and
// freezes their semantic intent lineage without mutating the prepare input.
func compileResultOverlayPlan(result *GenerateResult, language Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) error {
	if result == nil {
		return nil
	}
	plan, err := compileOverlayPlanForLanguage(result, language, planID, projectID, driveFolderID, canvas, plates, perSceneImages...)
	if err != nil {
		return err
	}
	result.OverlayPlan = plan
	var phraseItems []capabilityoverlay.OverlayItem
	if plan != nil {
		phraseItems = plan.Items
	}
	phraseBudget := capabilityoverlay.MeasurePhraseOverlayBudgetWithLimit(phraseItems, canvas.MaxPhraseOverlays)
	result.PhraseOverlayBudget = &phraseBudget
	if err := buildLocalizedOverlayPlans(result, language, planID, projectID, driveFolderID, canvas, plates, perSceneImages...); err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	if bundle, bundleErr := BuildSemanticRenderBundleFromResult(result, language, planID, plan.VideoID); bundleErr != nil {
		return fmt.Errorf("overlay plan: build semantic render bundle: %w", bundleErr)
	} else {
		result.SemanticRenderBundle = bundle
	}
	resolved := append([]capabilityoverlay.OverlayIntent(nil), result.OverlayIntents...)
	freezeOverlayIntents(resolved, plan.Items)
	result.ResolvedOverlayIntents = resolved
	return nil
}

// setOverlayDriveJobID keeps public broker identity separate from semantic
// idempotent plan identity; only the broker job owns the Drive tree.
func setOverlayDriveJobID(result *GenerateResult, jobID string) {
	if result == nil {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if result.OverlayPlan != nil {
		result.OverlayPlan.DriveJobID = jobID
		result.OverlayPlan.ResultJobID = jobID
	}
	for _, plan := range result.LocalizedOverlayPlans {
		if plan != nil {
			plan.DriveJobID = jobID
			plan.ResultJobID = jobID
		}
	}
}

// intentMatchesEntityItem joins a pre-timing entity intent to the plan item it
// materialized from. When the item carries an entity id (the content-addressed
// stable identity the canonical entity timeline stamped), the join is the id
// equality — spelling independent and exact, so a possessive surface, a
// normalized spelling or a localized card can no longer mis-join. Only a
// legacy plan item with no entity id falls back to the canonical-name
// comparison.
func intentMatchesEntityItem(intent capabilityoverlay.OverlayIntent, item capabilityoverlay.OverlayItem) bool {
	if _, ok := entityImageLayerForIntent(intent, item); ok {
		return true
	}
	if item.EntityID != "" {
		stable := capabilityentities.StableEntityID(intent.Entity.Type, intent.Entity.CanonicalName)
		return stable != "" && stable == item.EntityID
	}
	entityName := item.Text
	if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.Name) != "" {
		entityName = item.EntityRef.Name
	}
	return intent.Entity.CanonicalName == entityName
}

func entityImageLayerForIntent(intent capabilityoverlay.OverlayIntent, item capabilityoverlay.OverlayItem) (*capabilityoverlay.OverlayImageLayer, bool) {
	stable := capabilityentities.StableEntityID(intent.Entity.Type, intent.Entity.CanonicalName)
	if stable == "" {
		return nil, false
	}
	for index := range item.ImageLayers {
		if item.ImageLayers[index].EntityID == stable {
			return &item.ImageLayers[index], true
		}
	}
	return nil, false
}

func assetRefForImageLayer(item capabilityoverlay.OverlayItem, layer *capabilityoverlay.OverlayImageLayer) []capabilityoverlay.OverlayAssetRef {
	if layer == nil {
		return nil
	}
	for _, asset := range item.AssetRefs {
		if asset.AssetID == layer.AssetID {
			return []capabilityoverlay.OverlayAssetRef{asset}
		}
	}
	return nil
}

// freezeOverlayIntents promotes the pre-timing authoring bindings to the
// same certified timing/preset/asset facts emitted by OverlayPlan. The plan
// remains the renderer input; this projection keeps the persisted intent
// surface honest at the render boundary (no PENDING intents after lowering).
func freezeOverlayIntents(intents []capabilityoverlay.OverlayIntent, items []capabilityoverlay.OverlayItem) {
	for i := range intents {
		intent := &intents[i]
		for _, item := range items {
			matches := intent.SceneID == item.SceneID
			if intent.Source == capabilityoverlay.IntentSourceEntity {
				if layer, ok := entityImageLayerForIntent(*intent, item); ok && intent.SceneID == item.SceneID {
					// A composite is one render artifact but can represent several
					// entity intents. Freeze each intent against its own child asset,
					// preset and certified relative timing.
					intent.Kind = item.Kind
					intent.TemplateID = item.TemplateID
					intent.PresetID = layer.PresetID
					intent.AssetRefs = assetRefForImageLayer(item, layer)
					intent.Payload.AssetRefs = append([]capabilityoverlay.OverlayAssetRef(nil), intent.AssetRefs...)
					caption := strings.TrimSpace(layer.Caption)
					if caption == "" {
						caption = intent.Entity.CanonicalName
					}
					intent.Payload.Name = caption
					intent.Payload.Text = caption
					intent.StartMs = item.StartMs + layer.StartMS
					intent.EndMs = item.StartMs + layer.EndMS
					intent.TimingState = capabilityoverlay.TimingStateFrozen
					break
				}
				if len(item.ImageLayers) > 0 {
					matches = false
				} else {
					matches = matches && intentMatchesEntityItem(*intent, item)
				}
				if matches && intent.TimingState == capabilityoverlay.TimingStateFrozen {
					break
				}
			} else {
				matches = matches && intent.SourceText == item.Text
			}
			if !matches {
				continue
			}
			// The pre-timing intent is authored as an entity card because media
			// resolution may still be in flight. Once the final plan has a
			// verified image, the resolved intent must describe the exact layer
			// that RenderingGen receives: image_popup/entity_image, with no
			// display text. Keeping person_default/name here made the persisted
			// intent disagree with the image-only render plan and allowed a
			// downstream projection to recreate the old name-under-portrait card.
			intent.Kind = item.Kind
			intent.TemplateID = item.TemplateID
			intent.PresetID = item.PresetID
			intent.AssetRefs = append([]capabilityoverlay.OverlayAssetRef(nil), item.AssetRefs...)
			intent.Payload.AssetRefs = append([]capabilityoverlay.OverlayAssetRef(nil), item.AssetRefs...)
			if item.Kind == string(capabilityoverlay.KindEntityImage) {
				intent.Payload.Name = ""
				intent.Payload.Text = ""
			} else if strings.TrimSpace(item.Text) != "" {
				intent.Payload.Name = item.Text
			}
			intent.StartMs = item.StartMs
			intent.EndMs = item.EndMs
			intent.TimingState = capabilityoverlay.TimingStateFrozen
			break
		}
	}
}

// entityTimelineLanguage returns the language an EntityTimeline was certified
// for, or "" when there is no timeline.
func entityTimelineLanguage(timeline *capabilityentities.EntityTimeline) string {
	if timeline == nil {
		return ""
	}
	return timeline.Language
}

// compileOverlayPlanForLanguage is the side-effect-free per-language plan
// boundary shared by the source-language compatibility plan and translated
// plans. Drive routing stays in the application-only plan field and never
// crosses the RenderingGen queue contract.
func compileOverlayPlanForLanguage(result *GenerateResult, language Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	plan, err := CompileOverlayPlanWithPlates(result, language, canvas, planID, planID, projectID, plates, perSceneImages...)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		plan.DriveFolderID = strings.TrimSpace(driveFolderID)
	}
	return plan, nil
}

// buildLocalizedOverlayPlans compiles plans only for target languages that
// have translated voiceover timing. It never substitutes the source plan for
// a missing translation: a language without its own annotations/timing has no
// localized overlay artifact to publish.
func buildLocalizedOverlayPlans(result *GenerateResult, sourceLanguage Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) error {
	if result == nil {
		return nil
	}
	result.LocalizedOverlayPlans = nil
	seen := map[Language]struct{}{sourceLanguage: {}}
	for i := range result.Scenes {
		for language := range result.Scenes[i].Voiceover {
			if language == "" || language == sourceLanguage {
				continue
			}
			seen[language] = struct{}{}
		}
	}
	if len(seen) <= 1 {
		return nil
	}
	result.LocalizedOverlayPlans = make(map[Language]*capabilityoverlay.OverlayPlan, len(seen)-1)
	for language := range seen {
		if language == sourceLanguage {
			continue
		}
		localizedPlanID := planID + "-" + strings.ToLower(string(language))
		plan, err := compileOverlayPlanForLanguage(result, language, localizedPlanID, projectID, driveFolderID, canvas, plates, perSceneImages...)
		if err != nil {
			return fmt.Errorf("compile %s overlay plan: %w", language, err)
		}
		if plan != nil {
			result.LocalizedOverlayPlans[language] = plan
		}
	}
	return nil
}

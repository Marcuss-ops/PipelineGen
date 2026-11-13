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
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	overlaystatus "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
)

// compileResultOverlayPlan attaches the sealed source/localized plans and
// freezes their semantic intent lineage without mutating the prepare input.
func compileResultOverlayPlan(result *GenerateResult, language Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) error {
	if result == nil {
		return nil
	}
	// Build the verified asset bundle BEFORE lowering the plan. The bundle is
	// the planner's only entity-media source; building it afterwards left the
	// planner reading annotations that may already be cleared or localized.
	bundle, bundleErr := BuildSemanticRenderBundleFromResult(result, language, planID, planID, perSceneImages...)
	if bundleErr != nil {
		return fmt.Errorf("overlay plan: build semantic render bundle: %w", bundleErr)
	}
	if bundle != nil {
		// The hash describes the plan that is about to be compiled, not the
		// previous result's plan. Re-stamp it after sealing.
		bundle.OverlayPlanHash = ""
	}
	result.SemanticRenderBundle = bundle
	plan, err := compileOverlayPlanForLanguage(result, language, planID, projectID, driveFolderID, canvas, plates, perSceneImages...)
	if err != nil {
		return err
	}
	result.OverlayPlan = plan
	var phraseItems []capmodel.OverlayItem
	if plan != nil {
		phraseItems = plan.Items
		// Intake is planner-side telemetry carried beside the wire plan: it
		// explains the gap between the requested overlay mix and the items
		// that were actually admitted.
		result.OverlayPlanIntake = plan.Intake
	}
	phraseBudget := capabilityoverlay.MeasurePhraseOverlayBudgetWithLimit(phraseItems, canvas.MaxPhraseOverlays)
	result.PhraseOverlayBudget = &phraseBudget
	if err := buildLocalizedOverlayPlans(result, language, planID, projectID, driveFolderID, canvas, plates, perSceneImages...); err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	if bundle != nil {
		bundle.OverlayPlanHash = plan.Fingerprint
		result.SemanticRenderBundle = bundle
	}
	// The lineage is computed by the freeze pass itself, then projected into
	// the audit: the audit must describe the plan that was lowered, not the
	// pre-freeze intent set.
	resolved := append([]capmodel.OverlayIntent(nil), result.OverlayIntents...)
	lineage := freezeOverlayIntents(resolved, plan.Items)
	result.ResolvedOverlayIntents = resolved
	result.OverlayAudit = buildOverlayAuditWithLineage(bundle, plan, lineage)
	var admittedIdentityImages map[string]struct{}
	if result.SemanticProductionPlan != nil && len(result.SemanticProductionPlan.Identity) > 0 {
		admittedIdentityImages = result.SemanticProductionPlan.IdentitiesAdmittedForMedia()
	}
	applyRequestedEntityImageContract(result.OverlayAudit, bundle, plan, canvas.RequireEntityImages, canvas.EntityImagesRequireDrive, canvas.RequiredEntityTypes, admittedIdentityImages)
	overlaystatus.Update(planID, func(status *overlaystatus.Status) {
		status.PlanID = planID
		status.Stage = "plan_frozen"
		status.Audit = result.OverlayAudit
		status.Plan = overlayStatusPlanSummary(plan)
	})
	if err := enforceEntityImageGate(result.OverlayAudit); err != nil {
		overlaystatus.Update(planID, func(status *overlaystatus.Status) {
			status.Stage = "entity_gate_failed"
			status.Error = err.Error()
		})
		dumpOverlayGateDebug(result, planID)
		return err
	}
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
func intentMatchesEntityItem(intent capmodel.OverlayIntent, item capmodel.OverlayItem) bool {
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

func entityImageLayerForIntent(intent capmodel.OverlayIntent, item capmodel.OverlayItem) (*capmodel.OverlayImageLayer, bool) {
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

func assetRefForImageLayer(item capmodel.OverlayItem, layer *capmodel.OverlayImageLayer) []capmodel.OverlayAssetRef {
	if layer == nil {
		return nil
	}
	for _, asset := range item.AssetRefs {
		if asset.AssetID == layer.AssetID {
			return []capmodel.OverlayAssetRef{asset}
		}
	}
	return nil
}

// overlayIntentLineage is the intent→plan join summary. It exists because
// "PENDING" was an unexplained state in every artifact: 39 intents were
// authored, 31 stayed PENDING, and nothing said whether the entity lost
// admission or the planner rewrote the text. Authored = Frozen + Unresolved.
type overlayIntentLineage struct {
	Authored   int
	Frozen     int
	Unresolved int
	BySource   map[string]int
	Reasons    map[string]int
	Items      []overlayIntentResolution
}

type overlayIntentResolution struct {
	IntentID string
	SceneID  string
	Source   string
	Text     string
	Entity   string
	Reason   string
}

// freezeOverlayIntents promotes the pre-timing authoring bindings to the
// same certified timing/preset/asset facts emitted by OverlayPlan. The plan
// remains the renderer input; this projection keeps the persisted intent
// surface honest at the render boundary (no PENDING intents after lowering).
//
// It returns the lineage so an unresolved intent is reported with a reason
// instead of being left in the array as if it were a render instruction.
func freezeOverlayIntents(intents []capmodel.OverlayIntent, items []capmodel.OverlayItem) *overlayIntentLineage {
	lineage := &overlayIntentLineage{
		BySource: make(map[string]int),
		Reasons:  make(map[string]int),
	}
	for i := range intents {
		intent := &intents[i]
		for _, item := range items {
			matches := intent.SceneID == item.SceneID
			if intent.Source == capmodel.IntentSourceEntity {
				if layer, ok := entityImageLayerForIntent(*intent, item); ok && intent.SceneID == item.SceneID {
					// A composite is one render artifact but can represent several
					// entity intents. Freeze each intent against its own child asset,
					// preset and certified relative timing.
					intent.Kind = item.Kind
					intent.TemplateID = item.TemplateID
					intent.PresetID = layer.PresetID
					intent.AssetRefs = assetRefForImageLayer(item, layer)
					intent.Payload.AssetRefs = append([]capmodel.OverlayAssetRef(nil), intent.AssetRefs...)
					caption := strings.TrimSpace(layer.Caption)
					if caption == "" {
						caption = intent.Entity.CanonicalName
					}
					intent.Payload.Name = caption
					intent.Payload.Text = caption
					intent.StartMs = item.StartMs + layer.StartMS
					intent.EndMs = item.StartMs + layer.EndMS
					intent.TimingState = capmodel.TimingStateFrozen
					break
				}
				if len(item.ImageLayers) > 0 {
					matches = false
				} else {
					matches = matches && intentMatchesEntityItem(*intent, item)
				}
				if matches && intent.TimingState == capmodel.TimingStateFrozen {
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
			intent.AssetRefs = append([]capmodel.OverlayAssetRef(nil), item.AssetRefs...)
			intent.Payload.AssetRefs = append([]capmodel.OverlayAssetRef(nil), item.AssetRefs...)
			if item.Kind == string(capmodel.KindEntityImage) {
				intent.Payload.Name = ""
				intent.Payload.Text = ""
			} else if strings.TrimSpace(item.Text) != "" {
				intent.Payload.Name = item.Text
			}
			intent.StartMs = item.StartMs
			intent.EndMs = item.EndMs
			intent.TimingState = capmodel.TimingStateFrozen
			break
		}
	}
	for _, intent := range intents {
		lineage.record(intent, items)
	}
	return lineage
}

// record classifies one intent after the freeze pass. A FROZEN intent is a
// rendered item; anything else is a proposal whose reason is derived from what
// the plan actually contains.
func (l *overlayIntentLineage) record(intent capmodel.OverlayIntent, items []capmodel.OverlayItem) {
	if l == nil {
		return
	}
	l.Authored++
	source := string(intent.Source)
	if strings.TrimSpace(source) == "" {
		source = string(capmodel.IntentSourceEntity)
	}
	l.BySource[source]++
	if intent.TimingState == capmodel.TimingStateFrozen {
		l.Frozen++
		return
	}
	l.Unresolved++
	reason := overlayIntentUnresolvedReason(intent, items)
	l.Reasons[reason]++
	if len(l.Items) >= maxAuditUnresolvedItems {
		return
	}
	text := strings.TrimSpace(intent.SourceText)
	if text == "" {
		text = strings.TrimSpace(intent.Payload.Text)
	}
	l.Items = append(l.Items, overlayIntentResolution{
		IntentID: intent.IntentID,
		SceneID:  intent.SceneID,
		Source:   source,
		Text:     text,
		Entity:   strings.TrimSpace(intent.Entity.CanonicalName),
		Reason:   reason,
	})
}

// overlayIntentUnresolvedReason distinguishes the three ways an intent can
// fail to freeze: the entity lost admission, the source text was rewritten, or
// the join succeeded but the lowering did not freeze it (a defect, not a
// normal outcome).
func overlayIntentUnresolvedReason(intent capmodel.OverlayIntent, items []capmodel.OverlayItem) string {
	entitySource := intent.Source == capmodel.IntentSourceEntity || intent.Source == capmodel.IntentSourceEntityImage
	for _, item := range items {
		if item.SceneID != intent.SceneID {
			continue
		}
		if entitySource {
			if intentMatchesEntityItem(intent, item) {
				return OverlayIntentReasonTemplateNotMaterialized
			}
			continue
		}
		if text := strings.TrimSpace(intent.SourceText); text != "" && text == strings.TrimSpace(item.Text) {
			return OverlayIntentReasonTemplateNotMaterialized
		}
	}
	if entitySource {
		return OverlayIntentReasonEntityNotPlanned
	}
	return OverlayIntentReasonSourceTextNotInPlan
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
func compileOverlayPlanForLanguage(result *GenerateResult, language Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capmodel.OverlayPlan, error) {
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
	result.LocalizedOverlayPlans = make(map[Language]*capmodel.OverlayPlan, len(seen)-1)
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

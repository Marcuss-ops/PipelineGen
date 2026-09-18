// Package scriptgeneration — overlay_plan_freeze.go owns the render-boundary
// promotion of the pre-timing OverlayIntents: the exact join that binds each
// authoring intent to the plan item it materialized from, and the copy of that
// item's certified facts onto the intent.
//
// It lives beside overlay_plan.go (not inside it) because the join is the
// identity-sensitive half of the lowering: the intent is authored before media
// resolution, so it is matched to the final item by IDENTITY, never by the
// display text two independent surfaces happen to carry.
package scriptgeneration

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// intentMatchesEntityItem joins a pre-timing entity intent to the plan item it
// materialized from. When the item carries an entity id (the content-addressed
// stable identity the canonical entity timeline stamped), the join is the id
// equality — spelling independent and exact, so a possessive surface, a
// normalized spelling or a localized card can no longer mis-join. Only a
// legacy plan item with no entity id falls back to the canonical-name
// comparison.
func intentMatchesEntityItem(intent capabilityoverlay.OverlayIntent, item capabilityoverlay.OverlayItem) bool {
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
				matches = matches && intentMatchesEntityItem(*intent, item)
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

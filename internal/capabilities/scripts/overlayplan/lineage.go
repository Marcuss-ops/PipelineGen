// Package overlayplan — lineage.go owns the intent→plan join and the
// unresolved-intent classification extracted from the scripts root. The audit
// and the freeze boundary read this one projection instead of re-deriving why
// an authored intent did not materialize.
package overlayplan

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

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
	// The intent carries the canonical identity verbatim; the plan item may
	// carry it either directly or through its internal EntityRef. Check both
	// before deriving the content-addressed id, so a planner that emits the
	// readable identity ("person:corrado-augias") still joins by identity and
	// never falls back to matching display text.
	if canonical := strings.TrimSpace(intent.Entity.CanonicalEntityID); canonical != "" {
		if strings.TrimSpace(item.EntityID) == canonical {
			return true
		}
		if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.CanonicalEntityID) == canonical {
			return true
		}
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

// OverlayIntentLineage is the intent→plan join summary. It exists because
// "PENDING" was an unexplained state in every artifact: 39 intents were
// authored, 31 stayed PENDING, and nothing said whether the entity lost
// admission or the planner rewrote the text. Authored = Frozen + Unresolved.
type OverlayIntentLineage struct {
	Authored int
	Frozen   int
	// Covered counts an entity intent whose canonical identity has a card in
	// another scene. The card is reused, so the intent is resolved run-level
	// even though its own scene carries no timed item (TICKET-49).
	Covered    int
	Unresolved int
	BySource   map[string]int
	Reasons    map[string]int
	Items      []overlayIntentResolution
}

type overlayIntentResolution struct {
	IntentID          string
	SceneID           string
	Source            string
	Text              string
	Entity            string
	EntityID          string
	CanonicalEntityID string
	EntityType        string
	Reason            string
}

// FreezeOverlayIntents promotes the pre-timing authoring bindings to the
// same certified timing/preset/asset facts emitted by OverlayPlan. The plan
// remains the renderer input; this projection keeps the persisted intent
// surface honest at the render boundary (no PENDING intents after lowering).
//
// It returns the lineage so an unresolved intent is reported with a reason
// instead of being left in the array as if it were a render instruction.
func FreezeOverlayIntents(intents []capmodel.OverlayIntent, items []capmodel.OverlayItem) *OverlayIntentLineage {
	lineage := &OverlayIntentLineage{
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
func (l *OverlayIntentLineage) record(intent capmodel.OverlayIntent, items []capmodel.OverlayItem) {
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
	if entitySourceIntent(intent) && entityIntentCoveredByIdentity(intent, items) {
		// The same identity is on screen elsewhere in the run. Counting this as
		// unresolved made the audit contradict the image contract, which already
		// reports the portrait as planned (R30: Augias in scene-05).
		l.Covered++
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
		IntentID:          intent.IntentID,
		SceneID:           intent.SceneID,
		Source:            source,
		Text:              text,
		Entity:            strings.TrimSpace(intent.Entity.CanonicalName),
		EntityID:          capabilityentities.StableEntityID(intent.Entity.Type, intent.Entity.CanonicalName),
		CanonicalEntityID: strings.TrimSpace(intent.Entity.CanonicalEntityID),
		EntityType:        strings.TrimSpace(intent.Entity.Type),
		Reason:            reason,
	})
}

// overlayIntentUnresolvedReason distinguishes the three ways an intent can
// fail to freeze: the entity lost admission, the source text was rewritten, or
// the join succeeded but the lowering did not freeze it (a defect, not a
// normal outcome).
func overlayIntentUnresolvedReason(intent capmodel.OverlayIntent, items []capmodel.OverlayItem) string {
	entitySource := entitySourceIntent(intent)
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

func entitySourceIntent(intent capmodel.OverlayIntent) bool {
	return intent.Source == capmodel.IntentSourceEntity || intent.Source == capmodel.IntentSourceEntityImage
}

// entityIntentCoveredByIdentity reports whether ANY plan item carries the same
// canonical identity as the intent. It deliberately ignores the scene: the
// entity card is reused across scenes, unlike the timed occurrence.
func entityIntentCoveredByIdentity(intent capmodel.OverlayIntent, items []capmodel.OverlayItem) bool {
	canonical := strings.TrimSpace(intent.Entity.CanonicalEntityID)
	stable := capabilityentities.StableEntityID(intent.Entity.Type, intent.Entity.CanonicalName)
	for _, item := range items {
		if canonical != "" {
			if strings.TrimSpace(item.EntityID) == canonical {
				return true
			}
			if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.CanonicalEntityID) == canonical {
				return true
			}
		}
		if stable != "" {
			if strings.TrimSpace(item.EntityID) == stable {
				return true
			}
			if item.EntityRef != nil && strings.TrimSpace(item.EntityRef.EntityID) == stable {
				return true
			}
			for _, layer := range item.ImageLayers {
				if strings.TrimSpace(layer.EntityID) == stable {
					return true
				}
			}
		}
	}
	return false
}

// entityTimelineLanguage returns the language an EntityTimeline was certified
// for, or "" when there is no timeline.
func entityTimelineLanguage(timeline *capabilityentities.EntityTimeline) string {
	if timeline == nil {
		return ""
	}
	return timeline.Language
}

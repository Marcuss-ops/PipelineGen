package overlays

import (
	"sort"
	"strings"
)

// ── Named entity cards: the editorial budget's own arm ──────────────────────
//
// The entity resolver derives one card per certified entity occurrence on the
// EntityTimeline. A card WITH a verified image is promoted to KindEntityImage
// (image_popup) by the plan projection and is admitted by the image arm; a card
// WITHOUT one keeps its entity kind (entity_card / organization / location /
// concept) and is a text-only name card.
//
// That second shape had no arm in ApplyEditorialOverlayBudgetWithImageLimit, so
// retainOverlayItems — which keeps ONLY the indices named by an arm — silently
// dropped every text-only named card. The planner still caps them per scene
// (capNamedEntityCardsPerScene) and the payload still exposes the per-run
// "entities_with_text" group limit, so the drop contradicted both contracts:
// the cards were planned, capped and never rendered.
//
// Named entity text is therefore its own independent allowance, exactly like
// phrases, images, maps, numbers and brand text: it neither consumes nor is
// consumed by the other arms.
const (
	// MaxNamedEntityCardsPerRun bounds text-only named entity cards admitted to
	// one run. The per-scene ceiling (2) and the payload's entities_with_text
	// group limit are enforced downstream on the survivors. Twenty is twice the
	// ten-card acceptance corpus and keeps render fan-out bounded while still
	// covering every scene of a run up to the image arm's own 20-scene ceiling:
	// the scene-diversity pass below spends the allowance one card per scene
	// before admitting a second card from any scene.
	MaxNamedEntityCardsPerRun = 20
)

// namedEntityCardKind reports the text-only named entity card kinds, i.e. the
// kinds EntityTypeToKind produces for PERSON / ORG / GPE / concept entities.
func namedEntityCardKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case string(KindEntityCard), string(KindOrganization), string(KindLocation), string(KindConcept):
		return true
	}
	return false
}

// isTextOnlyNamedEntityCard reports a named entity card that carries no image.
// Image-backed cards are KindEntityImage after promotion and belong to the
// image arm, so the two arms never double-count the same item. Map plans are
// excluded explicitly so an entity card can never be admitted as a map.
func isTextOnlyNamedEntityCard(item OverlayItem) bool {
	if item.Map != nil || len(item.AssetRefs) > 0 {
		return false
	}
	return namedEntityCardKind(item.Kind)
}

// rankedNamedEntityCardIndices ranks and deduplicates the run's text-only named
// entity cards. The dedup key mirrors the entity resolver's own identity
// contract — the canonical entity id, falling back to the stable id — scoped to
// the scene, so one entity yields one card per scene even when two producers
// spell the same identity differently. Highest priority wins a collision, and
// the run allowance is applied to the ranked survivors.
func rankedNamedEntityCardIndices(items []OverlayItem) []int {
	indices := make([]int, 0)
	seen := make(map[string]int)
	for i, item := range items {
		if !isTextOnlyNamedEntityCard(item) {
			continue
		}
		key := namedEntityCardIdentity(item)
		if key == "" {
			continue
		}
		if current, ok := seen[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[current]) {
				seen[key] = i
			}
			continue
		}
		seen[key] = i
	}
	for _, index := range seen {
		indices = append(indices, index)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		if left, right := overlayItemPriority(items[indices[i]]), overlayItemPriority(items[indices[j]]); left != right {
			return left > right
		}
		return indices[i] < indices[j]
	})
	// Spend the run-level allowance across scenes before taking a second card
	// from any one scene, exactly like the grounded-phrase arm. Ranking decides
	// WHICH card each scene keeps, never WHETHER a later scene appears at all:
	// without this pass a fifteen-scene run with two entities per scene filled
	// the twenty-card allowance in scene order and silently dropped the name
	// cards of every scene after the tenth, so the run rendered fewer text
	// cards than the script had scenes.
	if len(indices) > 1 {
		seenScenes := make(map[string]struct{}, len(indices))
		diverse := make([]int, 0, len(indices))
		remaining := make([]int, 0, len(indices))
		for _, index := range indices {
			sceneID := strings.TrimSpace(items[index].SceneID)
			if sceneID == "" {
				remaining = append(remaining, index)
				continue
			}
			if _, ok := seenScenes[sceneID]; ok {
				remaining = append(remaining, index)
				continue
			}
			seenScenes[sceneID] = struct{}{}
			diverse = append(diverse, index)
		}
		indices = append(diverse, remaining...)
	}
	if len(indices) > MaxNamedEntityCardsPerRun {
		indices = indices[:MaxNamedEntityCardsPerRun]
	}
	return indices
}

// namedEntityCardIdentity returns the scene-scoped identity of one named entity
// card, or "" when the card carries no usable identity (such a card is never
// admitted: an anonymous name card has nothing to render).
func namedEntityCardIdentity(item OverlayItem) string {
	identity := ""
	if item.EntityRef != nil {
		identity = strings.TrimSpace(item.EntityRef.CanonicalEntityID)
		if identity == "" {
			identity = strings.TrimSpace(item.EntityRef.Name)
		}
	}
	if identity == "" {
		identity = strings.TrimSpace(item.EntityID)
	}
	if identity == "" {
		identity = strings.TrimSpace(item.ID)
	}
	if identity == "" {
		return ""
	}
	return strings.TrimSpace(item.SceneID) + ":" + strings.ToLower(identity)
}

package overlays

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
// (the retired per-scene cap) and the payload still exposes the per-run
// "entities_with_text" group limit, so the drop contradicted both contracts:
// the cards were planned, capped and never rendered.
//
// Named entity text is therefore its own independent allowance, exactly like
// phrases, images, maps, numbers and brand text: it neither consumes nor is
// consumed by the other arms.
const (
	// MaxNamedEntityCardsPerRun bounds text-only named entity cards admitted to
	// one run. The planner's per-scene ceiling and the payload's
	// entities_with_text group limit are enforced downstream on the survivors.
	// Fifty keeps render fan-out bounded while giving the scene-diversity pass
	// below enough room to cover a 50-scene run one card per scene before
	// admitting a second card from any scene.
	MaxNamedEntityCardsPerRun = 50
)

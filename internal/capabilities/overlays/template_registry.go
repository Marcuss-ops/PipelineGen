// Package overlays — template_registry.go owns the canonical semantic→concrete
// template table: the single place that knows every semantic entity's concrete
// layer shape AND the canonical primitive it terminates in. Kept separate from
// chronon.go so the compiler stays under the max-lines-per-file budget.
package overlays

// templateRegistry is the canonical semantic→concrete template table. It is
// the single place that knows every semantic entity's concrete layer shape
// AND the canonical primitive it terminates in — the golden workload shapes
// used by GoldenOverlayPlanV1 plus the extended semantic entity vocabulary.
//
// Every entry MUST declare its canonical Primitive: the compiler fails closed
// on a template without one, so a new entity can never be added without
// deciding which of Text/Image/Video/Shape it terminates in.
var templateRegistry = map[string]TemplateSpec{
	"BACKGROUND": {
		LayerType:  "image",
		Fit:        "cover",
		Primitive:  PrimitiveImage,
		FullCanvas: true,
	},
	"IMPORTANT_PHRASE": {
		LayerType: "text",
		Preset:    "title_centered",
		Primitive: PrimitiveText,
	},
	"IMPORTANT_WORD": {
		LayerType: "text",
		Preset:    "kinetic_word",
		Primitive: PrimitiveText,
	},
	"IMAGE_OVERLAY": {
		LayerType: "image",
		Fit:       "contain",
		BoxWidth:  260,
		BoxHeight: 260,
		Position:  []float64{380, 0},
		Primitive: PrimitiveImage,
	},
	// ── The canonical semantic entity vocabulary ────────────────────────
	// PERSON, LOCATION and QUOTE are the semantic spellings of the concrete
	// entity-card / quote templates below (aliases are pinned by tests);
	// NUMBER is a centered stat card; PRODUCT and LOGO are asset-driven
	// images (they fail closed when the item carries no asset).
	"PERSON": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	"NUMBER": {
		LayerType: "text",
		Preset:    "number",
		Primitive: PrimitiveText,
	},
	"QUOTE": {
		LayerType: "text",
		Preset:    "quote",
		Primitive: PrimitiveText,
	},
	"LOCATION": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	"PRODUCT": {
		LayerType: "image",
		Fit:       "contain",
		BoxWidth:  420,
		BoxHeight: 420,
		Position:  []float64{380, 0},
		Primitive: PrimitiveImage,
	},
	"LOGO": {
		LayerType: "image",
		Fit:       "contain",
		BoxWidth:  180,
		BoxHeight: 180,
		Position:  []float64{1060, 500},
		Primitive: PrimitiveImage,
	},
	// ── The Video and Shape primitives ──────────────────────────────────
	// VIDEO_BACKGROUND is the full-canvas video counterpart of BACKGROUND;
	// SHAPE is a full-canvas solid-color rect (Chronon "color" layer),
	// defaulting to a semi-transparent wash overridable via Params["color"].
	"VIDEO_BACKGROUND": {
		LayerType:  "video",
		Fit:        "cover",
		Primitive:  PrimitiveVideo,
		FullCanvas: true,
	},
	"SHAPE": {
		LayerType: "color",
		Color:     []float64{0, 0, 0, 0.35},
		Primitive: PrimitiveShape,
	},
	// LIGHT_LEAK is a composited video effect layer: a light-leak clip blended
	// over the background (screen blend by default). Its opacity and
	// loop/terminate behavior are per-item params; the source is the leak
	// clip (Video primitive → layer.source).
	"LIGHT_LEAK": {
		LayerType: "video",
		Fit:       "cover",
		Primitive: PrimitiveVideo,
		BlendMode: "screen",
	},
	// ── Concrete templates referenced by the kind registry / renderers ──
	// Entity cards (entity_card kind) compile to a text layer carrying the
	// entity name with the entity_card preset; the preset decides the final
	// portrait/name geometry. The optional image AssetRef becomes the card's
	// portrait (layer.Asset) alongside the name (layer.Text).
	"person_default": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	"org_default": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	"gpe_default": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	"concept_default": {
		LayerType: "text",
		Preset:    "entity_card",
		Primitive: PrimitiveText,
	},
	// Non-entity visual capabilities (ChrononOverlayRegistry kinds): a lower
	// third docks text in the bottom-left safe area, an image popup is a
	// contained image on the right, and a quote is centered text.
	"lower_third": {
		LayerType: "text",
		Preset:    "lower_third",
		Primitive: PrimitiveText,
	},
	"image_popup": {
		LayerType: "image",
		Fit:       "contain",
		BoxWidth:  260,
		BoxHeight: 260,
		Position:  []float64{380, 0},
		Primitive: PrimitiveImage,
	},
	"quote": {
		LayerType: "text",
		Preset:    "quote",
		Primitive: PrimitiveText,
	},
}

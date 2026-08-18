// Package overlays — template_registry.go owns the MINIMAL transport shape
// PipelineGen still needs after ADR-029: the content primitive (what a layer
// carries — text / image / video / shape) plus, ONLY for the preset-less
// primitives (backgrounds, shapes, effects, bare image treatments), the bare
// layer type and geometry.
//
// Preset-driven templates (every template with a semantic_role → preset
// mapping in SemanticOverlayResolver) carry ONLY their Primitive here: their
// layer `type` is derived by Chronon from the preset's supported_layer, and
// image geometry (box/fit/position) is owned by the preset (VisualPresetRegistry).
// PipelineGen no longer hardcodes IMAGE_OVERLAY/PERSON/… type or geometry.
package overlays

// templateRegistry is the minimal semantic→content table. Every entry MUST
// declare its canonical Primitive: the compiler fails closed on a template
// without one, so a new entity can never be added without deciding which of
// Text/Image/Video/Shape it terminates in.
//
// The six preset-less primitives (BACKGROUND, PRODUCT, LOGO, VIDEO_BACKGROUND,
// SHAPE, LIGHT_LEAK) additionally declare their bare transport shape, because
// they have no Chronon visual preset to derive it from. Everything else leaves
// LayerType/Fit/Box/Position zero: the compiler emits neither type nor geometry
// for preset-driven layers (Chronon owns them).
var templateRegistry = map[string]TemplateSpec{
	// ── Preset-driven templates: content primitive only ─────────────────
	"IMPORTANT_PHRASE": {Primitive: PrimitiveText},
	"IMPORTANT_WORD":   {Primitive: PrimitiveText},
	"IMAGE_OVERLAY":    {Primitive: PrimitiveImage},
	"PERSON":           {Primitive: PrimitiveText},
	"NUMBER":           {Primitive: PrimitiveText},
	"QUOTE":            {Primitive: PrimitiveText},
	"LOCATION":         {Primitive: PrimitiveText},

	// Concrete templates referenced by the kind registry / renderers.
	"person_default":  {Primitive: PrimitiveText},
	"org_default":     {Primitive: PrimitiveText},
	"gpe_default":     {Primitive: PrimitiveText},
	"concept_default": {Primitive: PrimitiveText},
	"lower_third":     {Primitive: PrimitiveText},
	"image_popup":     {Primitive: PrimitiveImage},
	"quote":           {Primitive: PrimitiveText},

	// ── Preset-less primitives: primitive + bare transport shape ────────
	// These have no semantic_role → preset mapping, so the render-plan layer
	// type and geometry are fundamental layer data (not a visual preset).
	"BACKGROUND": {
		LayerType:  "image",
		Fit:        "cover",
		Primitive:  PrimitiveImage,
		FullCanvas: true,
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
	"LIGHT_LEAK": {
		LayerType: "video",
		Fit:       "cover",
		Primitive: PrimitiveVideo,
		BlendMode: "screen",
	},
}

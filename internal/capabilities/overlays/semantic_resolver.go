// Package overlays — semantic_resolver.go owns the SINGLE editorial decision
// PipelineGen is allowed to make about HOW an overlay looks: the
// semantic_role → Chronon visual-preset mapping. Everything else visual
// (style, font, anchor/layout, animation defaults) is owned by Chronon's
// VisualPresetRegistry (ADR-029) and resolved there.
//
// PipelineGen decides COSA mostrare (semantic_role) and QUANDO (timing);
// Chronon decides COME renderizzarlo. This resolver is the whole bridge:
//
//	IMPORTANT_PHRASE → caption_card
//	IMPORTANT_WORD   → active_word_pop
//	PERSON           → lower_third_safe
//	ORG              → organization_card
//	LOCATION         → location_card
//	IMAGE            → image_focus_in
//
// The preset ids are owned by Chronon3d's VisualPresetRegistry; this package
// only references them by string. Adding a semantic role that maps to a new
// preset is a coordinated change (this table + the Chronon registry).
package overlays

// SemanticPreset is a canonical Chronon visual-preset id. The value space is
// owned by Chronon3d/include/chronon3d/registry/visual_preset_registry.cpp;
// PipelineGen never invents a preset here.
type SemanticPreset string

// The canonical Chronon preset ids (mirror of Chronon3d's VisualPresetRegistry
// seeds). These are the only presets a semantic role may resolve to.
const (
	PresetCaptionCard      SemanticPreset = "caption_card"
	PresetActiveWordPop    SemanticPreset = "active_word_pop"
	PresetSubtitleCard     SemanticPreset = "subtitle_card"
	PresetLowerThirdSafe   SemanticPreset = "lower_third_safe"
	PresetOrganizationCard SemanticPreset = "organization_card"
	PresetLocationCard     SemanticPreset = "location_card"
	PresetImageFocusIn     SemanticPreset = "image_focus_in"
)

// semanticPresetTable is the frozen semantic_role → preset table. It is the
// single source of truth for the editorial mapping; the compiler and every
// renderer resolve presets through it instead of hard-coding preset strings.
//
// Semantic roles that terminate in a preset-less primitive (BACKGROUND,
// VIDEO_BACKGROUND, SHAPE, LIGHT_LEAK, PRODUCT, LOGO) are intentionally absent:
// they compile to a bare layer whose appearance is the renderer's default, not
// a Chronon visual preset.
var semanticPresetTable = map[string]SemanticPreset{
	// ── The canonical semantic entity vocabulary ────────────────────────
	"IMPORTANT_PHRASE": PresetCaptionCard,
	"IMPORTANT_WORD":   PresetActiveWordPop,
	"NUMBER":           PresetActiveWordPop,
	"QUOTE":            PresetCaptionCard,
	"PERSON":           PresetLowerThirdSafe,
	"LOCATION":         PresetLocationCard,
	"IMAGE_OVERLAY":    PresetImageFocusIn,

	// ── Concrete templates referenced by the kind registry ──────────────
	"person_default":  PresetLowerThirdSafe,
	"org_default":     PresetOrganizationCard,
	"gpe_default":     PresetLocationCard,
	"concept_default": PresetCaptionCard,
	"lower_third":     PresetLowerThirdSafe,
	"image_popup":     PresetImageFocusIn,
	"quote":           PresetCaptionCard,
}

// SemanticOverlayResolver resolves a semantic role (OverlayItem.TemplateID
// spelling) to its canonical Chronon visual preset. It is stateless and safe
// for concurrent use.
type SemanticOverlayResolver struct{}

// PresetFor returns the canonical preset for a semantic role and whether the
// role has one. A preset-less primitive (backgrounds, shapes, effects) yields
// (false) — the caller emits a bare layer, never an invented preset.
func (SemanticOverlayResolver) PresetFor(semanticRole string) (string, bool) {
	p, ok := semanticPresetTable[semanticRole]
	return string(p), ok
}

// DefaultSemanticOverlayResolver is the process-wide resolver. The compiler
// and every renderer resolve through this single instance so every call site
// observes the same frozen semantic_role → preset mapping.
var DefaultSemanticOverlayResolver = SemanticOverlayResolver{}

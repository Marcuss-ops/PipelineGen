// Package overlays — renderers.go implements the per-kind renderers that
// materialize one OverlayItem into a ChrononLayer. Each renderer owns the
// kind-specific rules (which inputs are required, which preset/geometry it
// compiles to) and delegates the shared layer-shaping to buildLayer.
//
// The renderer is the "renderer" half of the registry contract declared in
// registry.go: OverlayPlan item.Kind → ChrononOverlayRegistry.Resolve(kind)
// → OverlayEntry.Renderer.Compile(item, plan) → ChrononLayer. PipelineGen
// owns WHAT appears and WHEN; Chronon owns the pixels; these renderers own
// HOW a kind becomes a layer (preset, fit, geometry, required inputs).
package overlays

import (
	"fmt"
	"math"
	"strings"
)

// Renderer compiles one OverlayItem of a given kind into a ChrononLayer.
type Renderer interface {
	// Name returns the canonical renderer name (e.g. "PersonCardRenderer").
	Name() string
	// Compile validates the item's required inputs and builds its layer.
	Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error)
}

// buildLayer is the shared layer-shaping helper every renderer delegates to.
// It projects the item's time range onto integer frames and applies the
// template spec plus per-item Params overrides (fit/position/box/preset).
// It does NOT enforce kind-specific required inputs — that is each renderer's
// job before delegating here.
func buildLayer(item OverlayItem, plan OverlayPlan, spec TemplateSpec) (ChrononLayer, error) {
	fps := plan.FPS
	frameAt := func(ms int64) int64 {
		return int64(math.Round(float64(ms) * float64(fps) / 1000.0))
	}
	layer := ChrononLayer{
		ID:             item.ID,
		Type:           spec.LayerType,
		Preset:         spec.Preset,
		Fit:            spec.Fit,
		BoxWidth:       spec.BoxWidth,
		BoxHeight:      spec.BoxHeight,
		Position:       spec.Position,
		StartFrame:     frameAt(item.StartMs),
		DurationFrames: frameAt(item.EndMs) - frameAt(item.StartMs),
	}
	if layer.DurationFrames <= 0 {
		return ChrononLayer{}, fmt.Errorf("overlay renderer: item %q compiles to a non-positive frame range", item.ID)
	}
	if strings.TrimSpace(item.Text) != "" {
		layer.Text = item.Text
	}
	// Text layers must declare a font (the runtime bundles none): the
	// canonical vendored fixture is the default, mirroring the compiler.
	if spec.Primitive == PrimitiveText {
		layer.Font = CanonicalTextFontPath
	}
	if v, ok := paramString(item.Params, "fit"); ok {
		layer.Fit = v
	}
	if v, ok := paramPosition(item.Params, "position"); ok {
		layer.Position = v
	}
	if v, ok := paramInt(item.Params, "box_width"); ok {
		layer.BoxWidth = v
	}
	if v, ok := paramInt(item.Params, "box_height"); ok {
		layer.BoxHeight = v
	}
	if v, ok := paramString(item.Params, "preset"); ok {
		layer.Preset = v
	}
	return layer, nil
}

// rendererForTemplate resolves a template id through templateRegistry and
// builds the layer. Used by the six concrete renderers below.
func rendererForTemplate(item OverlayItem, plan OverlayPlan, templateID, rendererName string) (ChrononLayer, error) {
	spec, ok := templateRegistry[templateID]
	if !ok {
		return ChrononLayer{}, fmt.Errorf("overlay renderer %s: template %q is not a chronon-compilable template", rendererName, templateID)
	}
	return buildLayer(item, plan, spec)
}

// requireText fails closed when the item has no text. Text-driven kinds
// (entity cards, lower thirds, quotes) never silently render an empty card.
func requireText(item OverlayItem, rendererName string) error {
	if strings.TrimSpace(item.Text) == "" {
		return fmt.Errorf("overlay renderer %s: item %q requires a non-empty text input", rendererName, item.ID)
	}
	return nil
}

// requireAsset fails closed when the item has no asset ref. Image popups
// need a pixel source; a missing ref is a hard error, never a blank popup.
func requireAsset(item OverlayItem, rendererName string) error {
	if len(item.AssetRefs) == 0 {
		return fmt.Errorf("overlay renderer %s: item %q requires at least one asset_refs input", rendererName, item.ID)
	}
	return nil
}

// ── The six canonical renderers ──────────────────────────────

// PersonCardRenderer renders an entity_card driven by a PERSON occurrence:
// the person's name over the entity_card preset (portrait optional).
type PersonCardRenderer struct{}

func (PersonCardRenderer) Name() string { return "PersonCardRenderer" }

func (PersonCardRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "PersonCardRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "person_default", "PersonCardRenderer")
}

// OrganizationCardRenderer renders an organization name card.
type OrganizationCardRenderer struct{}

func (OrganizationCardRenderer) Name() string { return "OrganizationCardRenderer" }

func (OrganizationCardRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "OrganizationCardRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "org_default", "OrganizationCardRenderer")
}

// LocationCardRenderer renders a place/location name card.
type LocationCardRenderer struct{}

func (LocationCardRenderer) Name() string { return "LocationCardRenderer" }

func (LocationCardRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "LocationCardRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "gpe_default", "LocationCardRenderer")
}

// LowerThirdRenderer renders a lower-third text banner (name/title docked in
// the lower-third safe area).
type LowerThirdRenderer struct{}

func (LowerThirdRenderer) Name() string { return "LowerThirdRenderer" }

func (LowerThirdRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "LowerThirdRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "lower_third", "LowerThirdRenderer")
}

// ImagePopupRenderer renders a contained image popup. It is asset-driven:
// the first asset ref is the pixel source.
type ImagePopupRenderer struct{}

func (ImagePopupRenderer) Name() string { return "ImagePopupRenderer" }

func (ImagePopupRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireAsset(item, "ImagePopupRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "image_popup", "ImagePopupRenderer")
}

// ConceptCardRenderer renders a generic concept card (EVENT/DATE/unknown
// entity types that are neither a person, organization nor place).
type ConceptCardRenderer struct{}

func (ConceptCardRenderer) Name() string { return "ConceptCardRenderer" }

func (ConceptCardRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "ConceptCardRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "concept_default", "ConceptCardRenderer")
}

// QuoteRenderer renders a centered quote.
type QuoteRenderer struct{}

func (QuoteRenderer) Name() string { return "QuoteRenderer" }

func (QuoteRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "QuoteRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "QUOTE", "QuoteRenderer")
}

// NumberRenderer renders a centered stat card for a spoken figure.
type NumberRenderer struct{}

func (NumberRenderer) Name() string { return "NumberRenderer" }

func (NumberRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "NumberRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "NUMBER", "NumberRenderer")
}

// ProductRenderer renders a product image popup. It is asset-driven: the
// first asset ref is the pixel source, exactly like ImagePopupRenderer.
type ProductRenderer struct{}

func (ProductRenderer) Name() string { return "ProductRenderer" }

func (ProductRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireAsset(item, "ProductRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "PRODUCT", "ProductRenderer")
}

// LogoRenderer renders a corner logo overlay. It is asset-driven: the first
// asset ref is the pixel source.
type LogoRenderer struct{}

func (LogoRenderer) Name() string { return "LogoRenderer" }

func (LogoRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireAsset(item, "LogoRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "LOGO", "LogoRenderer")
}

// ImportantPhraseRenderer renders a scene-local highlighted phrase.
type ImportantPhraseRenderer struct{}

func (ImportantPhraseRenderer) Name() string { return "ImportantPhraseRenderer" }

func (ImportantPhraseRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "ImportantPhraseRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "IMPORTANT_PHRASE", "ImportantPhraseRenderer")
}

// ImportantWordRenderer renders a scene-local kinetic keyword.
type ImportantWordRenderer struct{}

func (ImportantWordRenderer) Name() string { return "ImportantWordRenderer" }

func (ImportantWordRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireText(item, "ImportantWordRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "IMPORTANT_WORD", "ImportantWordRenderer")
}

// EntityImageRenderer renders an image bound to a canonical entity.
type EntityImageRenderer struct{}

func (EntityImageRenderer) Name() string { return "EntityImageRenderer" }

func (EntityImageRenderer) Compile(item OverlayItem, plan OverlayPlan) (ChrononLayer, error) {
	if err := requireAsset(item, "EntityImageRenderer"); err != nil {
		return ChrononLayer{}, err
	}
	return rendererForTemplate(item, plan, "IMAGE_OVERLAY", "EntityImageRenderer")
}

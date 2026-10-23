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
package render

// Primitive is the canonical primitive every semantic entity terminates in.
// PipelineGen classifies each entity as one of the four canonical primitives;
// RenderingGen owns the translation into the concrete Chronon layer type.
type Primitive string

const (
	// PrimitiveText is a preset-driven text layer.
	PrimitiveText Primitive = "text"
	// PrimitiveImage is a content-addressed image layer.
	PrimitiveImage Primitive = "image"
	// PrimitiveVideo is a content-addressed video layer.
	PrimitiveVideo Primitive = "video"
	// PrimitiveShape is a full-canvas solid-color rect layer.
	PrimitiveShape Primitive = "shape"
)

// TemplateSpec is the minimal transport shape PipelineGen still owns for a
// template: the content primitive (what a layer carries) plus, ONLY for the
// preset-less primitives, the bare layer type and geometry. Preset-driven
// templates declare only their Primitive — RenderingGen derives the rest.
type TemplateSpec struct {
	LayerType      string
	Fit            string
	BoxWidth       int
	BoxHeight      int
	Position       []float64
	RequiredInputs []string
	// Primitive is the canonical primitive (Text/Image/Video/Shape) this
	// template terminates in. Every template MUST declare one; the semantic
	// contract validator fails closed on an empty value.
	Primitive Primitive
	// FullCanvas stretches the layer to the full canvas for the whole clip
	// (backgrounds).
	FullCanvas bool
	// Color is the default RGBA fill for the Shape primitive.
	Color []float64
	// BlendMode is the default compositing blend for effect layers.
	BlendMode string
	// Opacity is the default layer opacity in [0,1]. Zero means "no override".
	Opacity float64
	// Loop controls a source whose duration is shorter than the layer window:
	// true loops the source, false (default) terminates the layer at the
	// source's end. There is never an implicit temporal stretch.
	Loop bool
}

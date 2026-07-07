// Package timeline — builder.go (FASE E, July 2026).
//
// FASE E — API Builder: fluent Go API for constructing timeline trees.
// Replaces the anti-pattern of "if frame" checks in content code with
// declarative sequence/layer construction via callback-based builders.
//
// The migration path:
//
//	// OLD (da eliminare)
//	if (ctx.frame >= Frame{30} && ctx.frame < Frame{90}) {
//	    l.text("TITLE");
//	}
//
//	// NEW (canonical)
//	BuildComposition("demo", Frame(200), 30.0, func(c *CompositionBuilder) {
//	    c.Sequence("title", SequenceSpec{From: 30, Duration: ptr(60)}, func(s *SequenceBuilder) {
//	        s.Layer("title", LayerKindText, func(l *LayerBuilder) {
//	            l.WithText("TITLE")
//	        })
//	    })
//	})
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of CompositionBuilder, SequenceBuilder,
// LayerBuilder, and the BuildComposition entry point. No other
// package may reimplement timeline tree construction.
//
// godlike/07: builders produce SequenceNode/LayerNode domain types
// that have NO temporal logic embedded — all temporal boundaries
// live in SequenceSpec, resolved by the TimelineResolver.
package timeline

// ── LayerProperties ────────────────────────────────────────────────

// LayerProperties carries layer-specific rendering data. The concrete
// interpretation depends on the layer's Kind. This is intentionally
// open-ended (any) for forward-compatibility with future layer types.
type LayerProperties struct {
	// Text is the text content for text layers.
	Text string `json:"text,omitempty"`

	// Opacity is the opacity animation track (Float64Lerp keyframes).
	// Uses local_frame for sampling per godlike/07.
	Opacity Animation[Float64Lerp] `json:"opacity,omitempty"`

	// Scale is the scale animation track.
	Scale Animation[Float64Lerp] `json:"scale,omitempty"`

	// PositionX / PositionY are position animation tracks.
	PositionX Animation[Float64Lerp] `json:"position_x,omitempty"`
	PositionY Animation[Float64Lerp] `json:"position_y,omitempty"`

	// Rotation is the rotation animation track (degrees).
	Rotation Animation[Float64Lerp] `json:"rotation,omitempty"`

	// Source is the media source path for video/audio layers.
	Source string `json:"source,omitempty"`

	// AssetID is the media asset registry reference.
	AssetID string `json:"asset_id,omitempty"`
}

// ── LayerBuilder ───────────────────────────────────────────────────

// LayerBuilder constructs a LayerNode with typed property setters.
// It acts as a bridge between the declarative builder API and the
// domain LayerNode type.
type LayerBuilder struct {
	node       LayerNode
	properties LayerProperties
}

// WithText sets the text content for text layers.
func (l *LayerBuilder) WithText(text string) *LayerBuilder {
	l.properties.Text = text
	return l
}

// WithOpacityAnim sets the opacity animation track.
// Keyframes use local_frame for sampling (godlike/07).
func (l *LayerBuilder) WithOpacityAnim(kfs ...Keyframe[Float64Lerp]) *LayerBuilder {
	l.properties.Opacity = NewFloat64Animation(kfs...)
	return l
}

// WithScaleAnim sets the scale animation track.
func (l *LayerBuilder) WithScaleAnim(kfs ...Keyframe[Float64Lerp]) *LayerBuilder {
	l.properties.Scale = NewFloat64Animation(kfs...)
	return l
}

// WithPositionAnim sets both X and Y position animation tracks.
func (l *LayerBuilder) WithPositionAnim(xKfs, yKfs []Keyframe[Float64Lerp]) *LayerBuilder {
	l.properties.PositionX = NewFloat64Animation(xKfs...)
	l.properties.PositionY = NewFloat64Animation(yKfs...)
	return l
}

// WithRotationAnim sets the rotation animation track.
func (l *LayerBuilder) WithRotationAnim(kfs ...Keyframe[Float64Lerp]) *LayerBuilder {
	l.properties.Rotation = NewFloat64Animation(kfs...)
	return l
}

// WithSource sets the media source path for video/audio layers.
func (l *LayerBuilder) WithSource(path string) *LayerBuilder {
	l.properties.Source = path
	return l
}

// WithAssetID sets the asset registry reference.
func (l *LayerBuilder) WithAssetID(id string) *LayerBuilder {
	l.properties.AssetID = id
	return l
}

// build finalizes the LayerNode. Called internally by SequenceBuilder.
func (l *LayerBuilder) build() LayerNode {
	l.node.Properties = l.properties
	return l.node
}

// ── SequenceBuilder ────────────────────────────────────────────────

// SequenceBuilder constructs a SequenceNode with child layers,
// sub-sequences, and media nodes. The callback-based API mirrors
// the C++ pattern from the plan §4.4/4.5.
type SequenceBuilder struct {
	seq SequenceNode
}

// Layer adds a layer to this sequence. The callback receives a
// LayerBuilder for configuring the layer's properties.
func (s *SequenceBuilder) Layer(name string, kind LayerKind, fn func(l *LayerBuilder)) {
	lb := &LayerBuilder{node: LayerNode{Name: name, Kind: kind}}
	fn(lb)
	s.seq.AddChild(lb.build())
}

// Sequence adds a nested sub-sequence. The callback receives a
// SequenceBuilder for configuring the sub-sequence's children.
// This enables arbitrarily deep nesting (plan §4.5).
func (s *SequenceBuilder) Sequence(name string, spec SequenceSpec, fn func(seq *SequenceBuilder)) {
	sb := &SequenceBuilder{seq: NewSequence(name, spec)}
	fn(sb)
	s.seq.AddChild(sb.build())
}

// Media adds a media node (video/audio source) to this sequence.
func (s *SequenceBuilder) Media(name, sourcePath string, mediaTime MediaTimeSpec) {
	s.seq.AddChild(MediaNode{
		Name:       name,
		SourcePath: sourcePath,
		MediaTime:  mediaTime,
	})
}

// build finalizes the SequenceNode. Called internally by
// SequenceBuilder and CompositionBuilder.
func (s *SequenceBuilder) build() SequenceNode {
	return s.seq
}

// ── CompositionBuilder ─────────────────────────────────────────────

// CompositionBuilder is the top-level fluent API entry point for
// constructing a Composition with sequences and layers. It wraps
// the composition being built and provides a Sequence() method
// that adds top-level sequences to the root.
type CompositionBuilder struct {
	comp *Composition
}

// Sequence adds a top-level sequence to the composition's root.
// The callback receives a SequenceBuilder for configuring the
// sequence's children.
func (c *CompositionBuilder) Sequence(name string, spec SequenceSpec, fn func(s *SequenceBuilder)) {
	sb := &SequenceBuilder{seq: NewSequence(name, spec)}
	fn(sb)
	c.comp.AddToRoot(sb.build())
}

// ── BuildComposition (canonical entry point) ───────────────────────

// BuildComposition is the canonical entry point for constructing a
// Composition with the fluent builder API. It creates a composition
// with the implicit root sequence (FASE B), then calls the callback
// to populate it.
//
// Example:
//
//	comp, err := BuildComposition("demo", Frame(200), 30.0, func(c *CompositionBuilder) {
//	    c.Sequence("intro", SequenceSpec{From: 0, Duration: ptr(30)}, func(s *SequenceBuilder) {
//	        s.Layer("logo", LayerKindText, func(l *LayerBuilder) {
//	            l.WithText("INTRO")
//	        })
//	    })
//	})
//
// Returns an error if duration <= 0 or fps is invalid (same as NewComposition).
func BuildComposition(name string, duration Frame, fps float64, fn func(c *CompositionBuilder)) (Composition, error) {
	comp, err := NewComposition(name, duration, fps)
	if err != nil {
		return Composition{}, err
	}

	cb := &CompositionBuilder{comp: &comp}
	fn(cb)

	return comp, nil
}

// ── Convenience helpers ────────────────────────────────────────────

// PtrFrame returns a pointer to the given Frame. Useful for optional
// fields like Duration and TrimAfter in SequenceSpec.
func PtrFrame(f Frame) *Frame {
	return &f
}

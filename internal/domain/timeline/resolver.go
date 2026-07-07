// Package timeline — resolver.go (FASE A, July 2026).
//
// TimelineNode is the Go sum-type for the timeline tree. In the C++
// spec this is a variant<SequenceNode, LayerNode, MediaNode>; in Go
// we use an interface with a private marker method.
//
// TimelineResolver is the central interface that resolves a
// Composition at a given global_frame into a flat ResolvedScene.
//
// ResolvedScene and ResolvedLayer are the output types that the
// renderer consumes — no temporal logic leaks into rendering.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of the TimelineNode interface hierarchy,
// TimelineResolver interface, ResolvedScene, and ResolvedLayer types.
//
// godlike/07: the renderer MUST receive only ResolvedScene — never
// access TimelineNode or TimeContext internals after resolution.
package timeline

import "fmt"

// ── TimelineNode (sum-type via interface) ──────────────────────────

// TimelineNode is the Go equivalent of variant<SequenceNode, LayerNode,
// MediaNode>. Each concrete type implements the private marker method
// timelineNodeMarker() so the set is closed at compile time.
//
// The three canonical variants:
//   - SequenceNode  (temporal container with children)
//   - LayerNode     (visual/text layer within a sequence)
//   - MediaNode     (video/audio source within a sequence)
type TimelineNode interface {
	timelineNodeMarker()
}

// ── LayerNode ──────────────────────────────────────────────────────

// LayerNode represents a visual or text layer within a sequence.
// It has no temporal logic — its activation is determined entirely
// by the parent sequence's TimeMappingResult.
//
// godlike/07: LayerNode has NO from/duration fields. Temporal
// boundaries live exclusively in the parent SequenceSpec.
type LayerNode struct {
	// Name is the layer identifier (e.g. "logo", "title", "background").
	Name string `json:"name"`

	// Kind classifies the layer type: text, image, shape, video, audio.
	Kind LayerKind `json:"kind"`

	// Properties carries layer-specific rendering data. The concrete
	// type depends on Kind.
	Properties any `json:"properties,omitempty"`
}

func (LayerNode) timelineNodeMarker() {}

// ── MediaNode ──────────────────────────────────────────────────────

// MediaNode represents a video or audio source within a sequence.
// It carries a MediaTimeSpec for source-frame resolution.
//
// godlike/07: the media source_frame is resolved via
// ResolveMediaFrame(local_frame, spec) — NEVER calculated inline
// in a renderer, video sampler, or FFmpeg adapter.
type MediaNode struct {
	// Name is the media clip identifier.
	Name string `json:"name"`

	// SourcePath is the filesystem path or URI to the source media.
	SourcePath string `json:"source_path"`

	// MediaTime defines how local_frame maps to source_frame.
	MediaTime MediaTimeSpec `json:"media_time"`

	// AssetID is an optional reference to the media asset registry.
	AssetID string `json:"asset_id,omitempty"`
}

func (MediaNode) timelineNodeMarker() {}

// ── LayerKind ──────────────────────────────────────────────────────

// LayerKind is the closed set of layer types in the rendering pipeline.
type LayerKind string

const (
	LayerKindText  LayerKind = "text"
	LayerKindImage LayerKind = "image"
	LayerKindShape LayerKind = "shape"
	LayerKindVideo LayerKind = "video"
	LayerKindAudio LayerKind = "audio"
)

// ── TimelineResolver ───────────────────────────────────────────────

// TimelineResolver is the central interface that resolves a Composition
// at a given global_frame into a flat ResolvedScene. Every temporal
// decision flows through this interface.
//
// godlike/06: the concrete implementation lives in the application
// layer; the domain defines only the contract.
//
// godlike/07: the renderer calls resolve() once per frame and receives
// a pre-resolved flat list. It MUST NOT re-derive activation, time
// mapping, or frame indices.
type TimelineResolver interface {
	// Resolve resolves the composition at the given global_frame into
	// a flat ResolvedScene. Returns nil if the composition is empty
	// or no content is active at this frame.
	Resolve(comp Composition, globalFrame Frame) *ResolvedScene
}

// ── ResolvedScene ──────────────────────────────────────────────────

// ResolvedScene is the flat output of TimelineResolver.Resolve().
// It contains only the layers that are active at the requested
// global_frame, each with its own resolved TimeContext.
//
// godlike/07: the renderer iterates ResolvedScene.Layers and renders
// each one. No temporal checks, no skip logic, no "is this active?"
// — the resolver already decided.
type ResolvedScene struct {
	// RootContext is the root-level TimeContext for this composition
	// frame. local_frame == global_frame for the root (FASE B).
	RootContext TimeContext `json:"root_context"`

	// Layers is the ordered flat list of active layers at this frame.
	// Each layer carries its own TimeContext with the correct
	// local_frame for that layer's scope.
	Layers []ResolvedLayer `json:"layers"`

	// GlobalFrame is the frame at which this scene was resolved.
	GlobalFrame Frame `json:"global_frame"`

	// ActiveCount is the number of active layers. Zero means an
	// empty frame (no content active at this global frame).
	ActiveCount int `json:"active_count"`
}

// IsEmpty returns true if no layers are active at this frame.
func (rs ResolvedScene) IsEmpty() bool {
	return rs.ActiveCount == 0
}

// ── ResolvedLayer ──────────────────────────────────────────────────

// ResolvedLayer is a single active layer with its resolved TimeContext.
// The renderer consumes this type — it receives the layer's content
// and the exact local_frame to use for animation, media sampling, etc.
type ResolvedLayer struct {
	// Node is the original TimelineNode (LayerNode or MediaNode)
	// that was resolved to produce this layer.
	Node TimelineNode `json:"node"`

	// TimeContext is the resolved temporal context for this layer.
	// local_frame is the correct frame for this layer's scope.
	TimeContext TimeContext `json:"time_context"`

	// Active is always true for entries in ResolvedScene.Layers
	// (inactive layers are filtered out). Present for defensive
	// assertions in the renderer.
	Active bool `json:"active"`
}

// ── Composition ─────────────────────────────────────────────────────

// Composition is the top-level container for a timeline. It owns the
// root sequence node and global metadata (duration, fps).
//
// FASE B: every Composition has an implicit root sequence where
// from=0, duration=composition.Duration, local_frame=global_frame.
// This ensures code without explicit sequences continues to work.
type Composition struct {
	// Name is the human-readable identifier for this composition.
	Name string `json:"name"`

	// Duration is the total length of the composition in frames.
	// Used to construct the implicit root sequence (FASE B).
	Duration Frame `json:"duration"`

	// FPS is the frames-per-second for the entire composition.
	FPS float64 `json:"fps"`

	// RootSequence is the implicit root sequence node that wraps
	// all top-level content. Created by NewComposition (FASE B).
	RootSequence SequenceNode `json:"root_sequence"`
}

// NewComposition creates a Composition with an implicit root sequence
// (FASE B). The root sequence has:
//   - from = 0
//   - duration = composition.Duration
//   - local_frame = global_frame
//
// Returns an error if duration <= 0 or fps is invalid.
func NewComposition(name string, duration Frame, fps float64) (Composition, error) {
	if err := validateFPS(fps); err != nil {
		return Composition{}, fmt.Errorf("composition %q: %w", name, err)
	}
	if duration <= 0 {
		return Composition{}, fmt.Errorf("composition %q: duration must be positive, got %d", name, duration)
	}

	return Composition{
		Name:     name,
		Duration: duration,
		FPS:      fps,
		RootSequence: NewSequence("root", SequenceSpec{
			From:     0,
			Duration: &duration,
		}),
	}, nil
}

// AddToRoot appends a TimelineNode to the root sequence's children.
func (c *Composition) AddToRoot(child TimelineNode) {
	c.RootSequence.AddChild(child)
}

// buildRootTimeContext creates the root TimeContext for a Composition
// at a given global_frame (FASE B: local_frame = global_frame).
// ScopePath is empty for the root — children append their names to
// form the hierarchy (e.g. "chapter", "chapter/title").
func buildRootTimeContext(comp Composition, globalFrame Frame) TimeContext {
	return TimeContext{
		GlobalFrame:   globalFrame,
		ParentFrame:   globalFrame,
		LocalFrame:    globalFrame,
		SequenceStart: 0,
		FPS:           comp.FPS,
		LocalSeconds:  float64(globalFrame.Value()) / comp.FPS,
		ScopePath:     "",
	}
}

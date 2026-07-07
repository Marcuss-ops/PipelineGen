// Package timeline — temporal_analysis.go (FASE F, July 2026).
//
// FASE F — Elimina legacy: TemporalAnalysis replaces the dangerous
// `duration == 1` static-sentinel anti-pattern with a structured
// dependency analysis.
//
// Before (anti-pattern):
//
//	bool is_static = layer.duration == Frame{1};
//
// After (canonical):
//
//	analysis := AnalyzeTemporalDependencies(node)
//	if analysis.IsStatic() { /* cache it */ }
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of TemporalAnalysis and the dependency
// analysis algorithms. No other package may reimplement static
// detection that bypasses this surface.
//
// godlike/07: a node is static ONLY when ALL three conditions hold:
//
//	!FrameDependent     — no animations with varying keyframes
//	!LocalTimeDependent — not inside a time-mapped sequence
//	!MediaTimeDependent — no media needing source-frame resolution
//
// The old `duration==1` heuristic satisfied none of these.
package timeline

// ── TemporalAnalysis ───────────────────────────────────────────────

// TemporalAnalysis captures the temporal dependencies of a TimelineNode
// subtree. It answers: "does this node depend on the current frame?"
//
// A node is static (cacheable forever) only when ALL three flags are
// false. This replaces the dangerous `duration == 1` heuristic from
// the legacy codebase.
type TemporalAnalysis struct {
	// FrameDependent is true when the subtree contains animations
	// whose output varies with the current frame. Examples:
	// opacity keyframes, scale interpolation, position tracks.
	FrameDependent bool `json:"frame_dependent"`

	// LocalTimeDependent is true when the subtree is inside a
	// SequenceNode. Sequences map parent_time → local_time, so
	// any node inside a sequence is inherently time-dependent.
	LocalTimeDependent bool `json:"local_time_dependent"`

	// MediaTimeDependent is true when the subtree contains a
	// MediaNode that needs source-frame resolution via
	// ResolveMediaFrame(). Media source frames depend on the
	// current local_frame and the MediaTimeSpec.
	MediaTimeDependent bool `json:"media_time_dependent"`
}

// IsStatic returns true when the node has zero temporal dependencies.
// A static node can be cached once and reused for every frame —
// it will always render identically.
func (ta TemporalAnalysis) IsStatic() bool {
	return !ta.FrameDependent && !ta.LocalTimeDependent && !ta.MediaTimeDependent
}

// IsFrameDependent returns true when any part of the subtree
// depends on the current frame value.
func (ta TemporalAnalysis) IsFrameDependent() bool {
	return ta.FrameDependent || ta.LocalTimeDependent || ta.MediaTimeDependent
}

// ── AnalyzeTemporalDependencies ────────────────────────────────────

// AnalyzeTemporalDependencies walks a TimelineNode subtree and computes
// its temporal dependencies. The analysis is recursive and accumulates
// dependencies from all children.
//
// Rules:
//   - SequenceNode → LocalTimeDependent (children inherit + may add more)
//   - LayerNode    → FrameDependent if it has non-empty animations
//   - MediaNode    → MediaTimeDependent
//
// godlike/07: the analysis is STATELESS and deterministic — same node
// always produces the same result. No side effects, no I/O.
func AnalyzeTemporalDependencies(node TimelineNode) TemporalAnalysis {
	ta := TemporalAnalysis{}
	analyzeNode(node, &ta)
	return ta
}

func analyzeNode(node TimelineNode, ta *TemporalAnalysis) {
	switch n := node.(type) {
	case SequenceNode:
		// Sequences ARE local-time-dependent by definition.
		// Their children inherit this dependency.
		ta.LocalTimeDependent = true
		for _, child := range n.Children {
			analyzeNode(child, ta)
		}

	case LayerNode:
		// Check if the layer has frame-varying animations.
		if props, ok := n.Properties.(LayerProperties); ok {
			if hasAnimations(props) {
				ta.FrameDependent = true
			}
		}

	case MediaNode:
		// Media nodes need source-frame resolution.
		ta.MediaTimeDependent = true
	}
}

// hasAnimations returns true if any LayerProperties field contains
// varying animations (2+ keyframes that vary with frame).
// Single-keyframe animations hold a constant value and are static.
func hasAnimations(props LayerProperties) bool {
	return props.Opacity.HasVaryingKeyframes() ||
		props.Scale.HasVaryingKeyframes() ||
		props.PositionX.HasVaryingKeyframes() ||
		props.PositionY.HasVaryingKeyframes() ||
		props.Rotation.HasVaryingKeyframes()
}

// ── Collective analysis helpers ────────────────────────────────────

// AnalyzeComposition runs temporal analysis on all top-level children
// of the composition's root sequence. Returns true if the composition
// as a whole has any temporal dependencies.
//
// Useful for pre-flight caching decisions: if a composition is fully
// static, the entire rendered output can be cached once.
func AnalyzeComposition(comp Composition) TemporalAnalysis {
	ta := TemporalAnalysis{}
	for _, child := range comp.RootSequence.Children {
		childTA := AnalyzeTemporalDependencies(child)
		ta.FrameDependent = ta.FrameDependent || childTA.FrameDependent
		ta.LocalTimeDependent = ta.LocalTimeDependent || childTA.LocalTimeDependent
		ta.MediaTimeDependent = ta.MediaTimeDependent || childTA.MediaTimeDependent
	}
	return ta
}

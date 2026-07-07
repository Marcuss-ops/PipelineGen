// Package timeline — composition.go (FASE A+B, July 2026).
//
// This file contains the recursive resolution logic: resolve_sequence()
// and resolve_timeline_node() are the core traversal functions that
// walk the timeline tree and produce a flat ResolvedScene.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of resolve_sequence(), resolve_timeline_node(),
// and ResolveCompositionFlat(). No other package may reimplement
// timeline tree traversal.
//
// godlike/07: ResolveCompositionFlat is the reference implementation
// of the TimelineResolver interface for testing and bootstrapping.
// Production implementations may optimize (parallel traversal, caching)
// but the contract is pinned here.
package timeline

// ── Recursive resolution ──────────────────────────────────────────

// ResolveCompositionFlat is a flat, recursive reference implementation
// of TimelineResolver. It walks the timeline tree depth-first and
// collects all active layers for the given global_frame.
//
// FASE B: the resolution starts from the implicit root sequence,
// so local_frame starts equal to global_frame.
func ResolveCompositionFlat(comp Composition, globalFrame Frame) *ResolvedScene {
	rootTime := buildRootTimeContext(comp, globalFrame)
	scene := &ResolvedScene{
		GlobalFrame: globalFrame,
		RootContext: rootTime,
	}

	resolveSequence(comp.RootSequence, rootTime, scene)

	scene.ActiveCount = len(scene.Layers)
	return scene
}

// resolveSequence processes a SequenceNode: maps the parent time
// through MapSequenceTime, then recursively processes children if
// the sequence is active.
func resolveSequence(seq SequenceNode, parentTime TimeContext, scene *ResolvedScene) {
	mapped := MapSequenceTime(seq.Spec, parentTime.LocalFrame)
	if !mapped.Active {
		return
	}

	childTime := parentTime.ChildContext(mapped, seq.Name)

	for _, child := range seq.Children {
		resolveTimelineNode(child, childTime, scene)
	}
}

// resolveTimelineNode dispatches a TimelineNode to its concrete
// resolver based on the Go type switch.
func resolveTimelineNode(node TimelineNode, timeCtx TimeContext, scene *ResolvedScene) {
	switch n := node.(type) {
	case SequenceNode:
		resolveSequence(n, timeCtx, scene)

	case LayerNode:
		scene.Layers = append(scene.Layers, ResolvedLayer{
			Node:        n,
			TimeContext: timeCtx,
			Active:      true,
		})

	case MediaNode:
		scene.Layers = append(scene.Layers, ResolvedLayer{
			Node:        n,
			TimeContext: timeCtx,
			Active:      true,
		})
	}
}

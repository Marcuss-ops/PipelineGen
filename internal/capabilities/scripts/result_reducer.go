// Package scriptgeneration — result_reducer.go owns the deterministic
// VidRush result reducer. Enrichment results can arrive out of order
// (scene-3, scene-1, scene-2); the reducer applies them in canonical
// SceneIndex order so the merged SpecScene is stable regardless of arrival
// ordering. It never mutates shared scene state: every scene is projected
// into a fresh envelope through the per-scene merger port.
package scriptgeneration

import (
	"sort"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// SegmentSceneMerger projects one immutable segment enrichment result onto its
// matching canonical scene. Implementations must return a new scene value and
// must never mutate the input scene. The reducer owns ordering and matching;
// the merger owns the per-scene projection (annotations, bindings).
type SegmentSceneMerger interface {
	Merge(scene scriptpkg.SpecScene, result scriptpkg.VidRushSegmentResult) scriptpkg.SpecScene
}

// VidRushResultReducer deterministically merges immutable segment enrichment
// results into a canonical SpecScene envelope. It is safe to reuse across runs
// and does not retain state between Reduce calls.
type VidRushResultReducer struct {
	merger SegmentSceneMerger
}

// NewVidRushResultReducer constructs a reducer that applies each result to its
// scene through the supplied merger. A nil merger is allowed: results are then
// ordered and matched but leave scenes unchanged.
func NewVidRushResultReducer(merger SegmentSceneMerger) *VidRushResultReducer {
	return &VidRushResultReducer{merger: merger}
}

// Reduce merges the given segment results into a copy of spec in canonical
// SceneIndex order. Results are first sorted by Position (then SceneID, then
// SegmentID as deterministic tie-breakers), then each result is matched to its
// scene using the canonical identity precedence (SegmentID, SceneID, Position)
// and applied through the merger. The input envelope is never mutated.
func (r *VidRushResultReducer) Reduce(spec scriptpkg.SpecSceneOutput, results []scriptpkg.VidRushSegmentResult) scriptpkg.SpecSceneOutput {
	out := scriptpkg.SpecSceneOutput{
		Version:           spec.Version,
		Scenes:            make([]scriptpkg.SpecScene, len(spec.Scenes)),
		VisualAssignments: append([]mediadomain.VisualAssignment(nil), spec.VisualAssignments...),
	}
	copy(out.Scenes, spec.Scenes)

	ordered := append([]scriptpkg.VidRushSegmentResult(nil), results...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Position != ordered[j].Position {
			return ordered[i].Position < ordered[j].Position
		}
		if ordered[i].SceneID != ordered[j].SceneID {
			return ordered[i].SceneID < ordered[j].SceneID
		}
		return ordered[i].SegmentID < ordered[j].SegmentID
	})

	for i := range out.Scenes {
		seg, ok := matchSceneSegment(out.Scenes[i], ordered)
		if !ok {
			continue
		}
		if isStaleSegment(out.Scenes[i], seg) {
			continue
		}
		if r.merger == nil {
			continue
		}
		out.Scenes[i] = r.merger.Merge(out.Scenes[i], seg)
	}
	return out
}

// isStaleSegment reports whether seg was derived from different scene content
// than the current scene and must therefore be discarded before the merge. A
// result is stale when its TextHash is present and differs from the current
// scene's content hash (SceneTextHash of scene.Text); an empty TextHash has no
// identity to compare and is left to the caller's upstream fencing.
//
// Revision fencing is owned by VidRushIncrementalCoordinator, which compares
// each result's (revision, text_hash) against the latest committed identity
// before results ever reach the reducer; this check is the merge-side content
// fence that keeps a scene from being annotated with media extracted from an
// older text version.
func isStaleSegment(scene scriptpkg.SpecScene, seg scriptpkg.VidRushSegmentResult) bool {
	if seg.TextHash == "" {
		return false
	}
	return seg.TextHash != SceneTextHash(scene.Text)
}

// matchSceneSegment returns the first result (in the already-ordered slice)
// that matches the scene, using the same identity precedence as the batch
// path: SegmentID first, then SceneID, then Position.
func matchSceneSegment(scene scriptpkg.SpecScene, ordered []scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, bool) {
	for _, seg := range ordered {
		if scene.SegmentID != "" && scene.SegmentID == seg.SegmentID {
			return seg, true
		}
		if scene.ID != "" && scene.ID == seg.SceneID {
			return seg, true
		}
		if scene.SegmentID == "" && scene.ID == "" && scene.Index == seg.Position {
			return seg, true
		}
	}
	return scriptpkg.VidRushSegmentResult{}, false
}

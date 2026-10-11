package scriptgeneration

// Package scriptgeneration — scene_intelligence_projection.go owns the
// shadow-mode projection of the SceneIntelligenceManifest onto scenes.
//
// Analysis-only: it saves per-scene signals (density budget hints, visual
// candidate kinds, recap eligibility) WITHOUT changing any video, overlay
// plan, narration or timeline. Production planners keep reading their
// existing inputs; when the feature is disabled the projection is a no-op
// returning nil, so job and overlay outputs are byte-identical with or
// without it. Rendering wiring belongs to a later explicit phase.

import (
	"sort"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SIProjectionConfig gates the shadow projection. Disabled is the default:
// no manifest is read, nothing is projected, outputs are unchanged.
type SIProjectionConfig struct {
	Enabled bool
}

// SI SceneProjection is one scene's saved shadow signals.
type SISceneProjection struct {
	SceneID string
	// VisualBudget is the density hint for Visual Admission ("low" means
	// few simultaneous overlays on a cognitively dense scene).
	VisualBudget string
	DensityScore float64
	// CandidateKinds names the planner-input categories this scene earned,
	// drawn ONLY from existing planner inputs (phrase, keyword, number,
	// multi_entity, timeline_labels): no new renderer kinds.
	CandidateKinds []string
	// TimelineRecipeEligible is true when temporal events are orderable
	// (absolute years present), so a timeline recipe MAY be built later.
	TimelineRecipeEligible bool
	// RecapEligible marks scenes usable as recap sources (covered bullets
	// exist); the recap builder still only reuses already-covered content.
	RecapEligible bool
}

// ProjectSceneIntelligence projects an already-validated manifest onto
// scenes. It is pure and deterministic (sorted scene order). A nil manifest
// or a disabled config returns nil: shadow mode never fabricates signals.
func ProjectSceneIntelligence(m *scriptpkg.SceneIntelligenceManifest, cfg SIProjectionConfig, recapScenes map[string]bool) []SISceneProjection {
	if !cfg.Enabled || m == nil {
		return nil
	}
	ids := m.SceneIDs()
	out := make([]SISceneProjection, 0, len(ids))
	for _, id := range ids {
		sig := m.SignalsFor(id)
		if sig == nil {
			continue
		}
		p := SISceneProjection{
			SceneID:      id,
			VisualBudget: sig.Density.SuggestedVisualBudget,
			DensityScore: sig.Density.Score,
		}
		kinds := map[string]struct{}{}
		for _, c := range sig.Comparisons {
			if c.CandidateVisual == "value_comparison" {
				kinds["number"] = struct{}{}
			} else {
				kinds["multi_entity"] = struct{}{}
			}
		}
		if len(sig.Causal) > 0 {
			kinds["keyword"] = struct{}{}
		}
		if len(sig.TemporalEvents) > 0 {
			kinds["keyword"] = struct{}{}
			for _, e := range sig.TemporalEvents {
				if e.NormalizedYear != nil {
					p.TimelineRecipeEligible = true
					break
				}
			}
		}
		if len(sig.Relations) > 0 {
			kinds["multi_entity"] = struct{}{}
		}
		for k := range kinds {
			p.CandidateKinds = append(p.CandidateKinds, k)
		}
		sort.Strings(p.CandidateKinds)
		p.RecapEligible = recapScenes[id]
		out = append(out, p)
	}
	if out == nil {
		return nil
	}
	return out
}

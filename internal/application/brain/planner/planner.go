// Package planner is the canonical home of scene visual planning
// for the Brain capability.
//
// godlike/06 SSOT: the SceneVisualPlanner is the single owner of
// the (ranked candidates + scene -> SceneVisualPlan) transformation.
// It performs no IO and depends only on the brain types and stdlib.
package planner

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
)

// SceneVisualPlanner is the canonical port that projects ranked
// candidates onto the visual layers of a single scene.
type SceneVisualPlanner interface {
	Plan(
		ctx context.Context,
		scene brain.SceneRequest,
		intent brain.VisualIntent,
		rankedCandidates []brain.Candidate,
	) (brain.SceneVisualPlan, error)
	Version() string
}

// defaultPlanner is the canonical pure implementation.
type defaultPlanner struct{}

// NewDefaultPlanner returns the canonical scene visual planner.
func NewDefaultPlanner() SceneVisualPlanner {
	return &defaultPlanner{}
}

// Compile-time assertion: defaultPlanner satisfies SceneVisualPlanner.
var _ SceneVisualPlanner = (*defaultPlanner)(nil)

// Version returns the canonical planner version.
func (p *defaultPlanner) Version() string {
	return "scene-planner-v1"
}

// Plan assigns one candidate to each requested slot, in order.
// The planner never invents candidates: if there are fewer candidates
// than slots, the remaining slots are left empty and the plan status
// is set to "partial". Materialization state is copied from the
// candidate verbatim.
func (p *defaultPlanner) Plan(_ context.Context, scene brain.SceneRequest, intent brain.VisualIntent, rankedCandidates []brain.Candidate) (brain.SceneVisualPlan, error) {
	plan := brain.SceneVisualPlan{
		SceneID: scene.ID,
		Intent:  intent,
		Status:  "success",
	}

	used := 0
	for _, slot := range scene.Slots {
		if used >= len(rankedCandidates) {
			plan.Status = "partial"
			break
		}
		c := rankedCandidates[used]
		plan.Layers = append(plan.Layers, brain.VisualLayer{
			Slot:                 slot,
			CandidateID:          c.ID,
			AssetID:              c.AssetID,
			BindingID:            c.BindingID,
			StartMs:              0,
			EndMs:                scene.DurationMS,
			MaterializationState: c.MaterializationState,
			Provider:             c.Provider,
			Score:                c.Score,
		})
		used++
	}

	if len(plan.Layers) == 0 {
		plan.Status = "empty"
	}

	// Confidence is a simple function of how many requested slots
	// received a layer. Future planners may use intent uncertainty.
	if len(scene.Slots) > 0 {
		plan.Confidence = float64(len(plan.Layers)) / float64(len(scene.Slots))
	}

	return plan, nil
}

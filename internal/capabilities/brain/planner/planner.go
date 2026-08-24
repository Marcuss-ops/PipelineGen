// Package planner is the canonical home of scene visual planning
// for the Brain capability.
//
// godlike/06 SSOT: the SceneVisualPlanner is the single owner of
// the (ranked candidates + scene -> SceneVisualPlan) transformation.
// It performs no IO and depends only on the brain types and stdlib.
package planner

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

// SlotCandidateSampler is the canonical pure implementation of the
// scene visual planner. It partitions candidates by slot, enforces
// mandatory gates per slot, ranks candidates per slot, applies
// diversity (no duplicate asset across slots unless unavoidable), and
// selects one winner per requested slot.
//
// godlike/06 SSOT: the sampler never invents candidates; slots that
// cannot be filled are left empty and the plan status becomes
// "partial".
type SlotCandidateSampler struct{}

// NewDefaultPlanner returns the canonical scene visual planner.
func NewDefaultPlanner() SceneVisualPlanner {
	return NewSlotCandidateSampler()
}

// NewSlotCandidateSampler returns a SlotCandidateSampler.
func NewSlotCandidateSampler() SceneVisualPlanner {
	return &SlotCandidateSampler{}
}

// Compile-time assertion: SlotCandidateSampler satisfies SceneVisualPlanner.
var _ SceneVisualPlanner = (*SlotCandidateSampler)(nil)

// Version returns the canonical planner version.
func (s *SlotCandidateSampler) Version() string {
	return media.VersionSlotSampler
}

// Plan assigns one candidate to each requested slot. Candidates are
// filtered by slot media-type compatibility and mandatory gates, then
// selected in ranked order while preserving asset diversity across
// slots.
func (s *SlotCandidateSampler) Plan(_ context.Context, scene brain.SceneRequest, intent brain.VisualIntent, rankedCandidates []brain.Candidate) (brain.SceneVisualPlan, error) {
	plan := brain.SceneVisualPlan{
		SceneID: scene.ID,
		Intent:  intent,
		Status:  "success",
		Layers:  make([]brain.VisualLayer, 0, len(scene.Slots)),
	}

	usedAssets := make(map[string]struct{})

	for _, slot := range scene.Slots {
		var winner *brain.Candidate
		var fallback *brain.Candidate

		for i := range rankedCandidates {
			c := &rankedCandidates[i]
			if !s.passesGates(c) {
				continue
			}
			if !media.IsMediaTypeAllowed(slot, c.MediaType) {
				continue
			}

			if _, used := usedAssets[c.AssetID]; !used {
				winner = c
				break
			}
			if fallback == nil {
				fallback = c
			}
		}

		if winner == nil {
			winner = fallback
		}

		if winner == nil {
			continue
		}

		usedAssets[winner.AssetID] = struct{}{}
		plan.Layers = append(plan.Layers, brain.VisualLayer{
			Slot:                 slot,
			CandidateID:          winner.ID,
			AssetID:              winner.AssetID,
			BindingID:            winner.BindingID,
			StartMs:              0,
			EndMs:                scene.DurationMS,
			MaterializationState: winner.MaterializationState,
			Provider:             winner.Provider,
			Score:                winner.Score,
		})
	}

	if len(plan.Layers) == 0 {
		plan.Status = "empty"
	} else if len(plan.Layers) < len(scene.Slots) {
		plan.Status = "partial"
	}

	if len(scene.Slots) > 0 {
		plan.Confidence = float64(len(plan.Layers)) / float64(len(scene.Slots))
	}

	return plan, nil
}

// passesGates enforces the mandatory gates every candidate must pass
// before being assigned to any slot. The ranker already filters most
// problematic candidates; the sampler keeps a small, deterministic
// safety net for media-type, rights and materialization.
func (s *SlotCandidateSampler) passesGates(c *brain.Candidate) bool {
	// Rights: fail-closed when an explicit denial/expiry is known.
	switch c.RightsStatus {
	case "denied", "expired":
		return false
	}
	// Materialization: never assign a candidate whose materialization
	// explicitly failed.
	if c.MaterializationState == "failed" {
		return false
	}
	return true
}

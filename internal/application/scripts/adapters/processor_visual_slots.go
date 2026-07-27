package adapters

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	visual "github.com/Marcuss-ops/PipelineGen/internal/application/visual"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// VisualSlotsProcessor resolves timeline-level intro and post-segment clips
// into the same SpecScene envelope consumed by persistence and rendering.
type VisualSlotsProcessor struct {
	planner visual.Planner
}

type closedPlanner struct{ planner VisualCandidatePlanner }

func NewClosedVisualPlanner(planner VisualCandidatePlanner) visual.Planner {
	if planner == nil {
		return nil
	}
	return closedPlanner{planner: planner}
}

func (p closedPlanner) Select(ctx context.Context, req visual.PlannerRequest) (visual.PlannerResult, error) {
	if p.planner == nil {
		return visual.PlannerResult{}, nil
	}
	candidates := make([]mediamemory.CandidateOption, 0, len(req.CandidateIDs))
	for _, id := range req.CandidateIDs {
		candidates = append(candidates, mediamemory.CandidateOption{AssetID: id, CandidateID: id})
	}
	id, err := p.planner.Select(ctx, VisualSelectionRequest{SegmentID: req.SegmentID, Slot: mediadomain.SlotKind(req.Slot), Candidates: candidates, VisualIntent: req.Goal})
	if err != nil {
		return visual.PlannerResult{}, err
	}
	return visual.PlannerResult{SelectedAssetIDs: []string{id}}, nil
}

func NewVisualSlotsProcessor(planner visual.Planner) *VisualSlotsProcessor {
	return &VisualSlotsProcessor{planner: planner}
}

func (p *VisualSlotsProcessor) Name() ProcessorName { return ProcessorVisualSlots }
func (p *VisualSlotsProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *VisualSlotsProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || (plan.MediaPlan.Intro == nil && len(plan.MediaPlan.PostSegments) == 0) {
		return &PostProcessResult{}, nil
	}
	seed := int64(0)
	if plan.MediaPlan.Variation != nil {
		seed = plan.MediaPlan.Variation.Seed
	}
	var all []mediadomain.VisualAssignment
	var warnings []string
	if plan.MediaPlan.Intro != nil {
		res, err := resolveSlot(ctx, plan, input, "", mediadomain.VisualSlotIntro, *plan.MediaPlan.Intro, seed, p.planner)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Assignments...)
		warnings = append(warnings, res.Warnings...)
	}
	for _, post := range plan.MediaPlan.PostSegments {
		res, err := resolveSlot(ctx, plan, input, post.SegmentID, mediadomain.VisualSlotPostSegment, post.VisualSlotPlan, seed, p.planner)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Assignments...)
		warnings = append(warnings, res.Warnings...)
	}
	updated := input.SpecScene
	updated.VisualAssignments = append([]mediadomain.VisualAssignment(nil), all...)
	return &PostProcessResult{VisualAssignments: all, UpdatedSpecScene: updated, Warnings: warnings, Changed: len(all) > 0}, nil
}

func resolveSlot(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, segmentID string, slot mediadomain.VisualSlot, spec mediadomain.VisualSlotPlan, seed int64, planner visual.Planner) (visual.Result, error) {
	ids := append([]string(nil), spec.CandidateAssetIDs...)
	for _, clip := range spec.Clips {
		if clip.AssetID != "" {
			ids = append(ids, clip.AssetID)
		}
	}
	candidates := make([]visual.Candidate, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			candidates = append(candidates, visual.Candidate{AssetID: id})
		}
	}
	return visual.Resolve(ctx, visual.Request{SceneID: sceneIDForSegment(input.SpecScene.Scenes, segmentID), SegmentID: segmentID, Slot: slot, Plan: spec, Candidates: candidates, Seed: seed, PromptVersion: plan.PromptVersion, ForceRefresh: plan.MediaPlan.ForceRefreshBindings}, planner)
}

func sceneIDForSegment(scenes []scriptpkg.SpecScene, segmentID string) string {
	for _, scene := range scenes {
		if segmentID != "" && scene.SegmentID == segmentID {
			return scene.ID
		}
	}
	if len(scenes) > 0 {
		return scenes[0].ID
	}
	return ""
}

package adapters

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	visual "github.com/Marcuss-ops/PipelineGen/internal/capabilities/visual"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
func (p *VisualSlotsProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && (plan.MediaPlan.Intro != nil || len(plan.MediaPlan.PostSegments) > 0) {
		// A requested timeline is part of the caller's media contract. Invalid
		// manual positions, duplicates, or unavailable closed candidates must
		// fail the job instead of producing a successful document with missing
		// visual assignments.
		return ProcessorRequired
	}
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
	if plan.MediaPlan.Intro != nil && !sceneIsFixedMedia(input.SpecScene, "", "", 0) {
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
	projectPostSegmentClipBindings(updated.Scenes, all)
	return &PostProcessResult{VisualAssignments: all, UpdatedSpecScene: updated, Warnings: warnings, Changed: len(all) > 0}, nil
}

// projectPostSegmentClipBindings exposes the primary post-segment clip in the
// per-scene binding surface as well as in the independent timeline contract.
// The timeline remains the authoritative place for multiple clips (for
// example the two Sugar Ray Robinson outro clips); ClipBinding is singular,
// so it carries the first clip while VisualAssignments retains every clip and
// its exact position.
func projectPostSegmentClipBindings(scenes []scriptpkg.SpecScene, assignments []mediadomain.VisualAssignment) {
	for _, assignment := range assignments {
		if assignment.Slot != mediadomain.VisualSlotPostSegment || assignment.AssetID == "" || assignment.Position != 0 {
			continue
		}
		for i := range scenes {
			matchesScene := assignment.SceneID != "" && scenes[i].ID == assignment.SceneID
			matchesSegment := assignment.SegmentID != "" && scenes[i].SegmentID == assignment.SegmentID
			if !matchesScene && !matchesSegment {
				continue
			}
			if !scenes[i].AllowsMediaReplacement() {
				continue
			}
			scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
				ClipID:     assignment.AssetID,
				StartMs:    assignment.StartMs,
				EndMs:      assignment.StartMs + assignment.DurationMs,
				DurationMs: assignment.DurationMs,
			}
			break
		}
	}
}

func resolveSlot(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, segmentID string, slot mediadomain.VisualSlot, spec mediadomain.VisualSlotPlan, seed int64, planner visual.Planner) (visual.Result, error) {
	if sceneIsFixedMedia(input.SpecScene, "", segmentID, 0) {
		return visual.Result{}, nil
	}
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
	return visual.Resolve(ctx, visual.Request{SceneID: sceneIDForSegment(input.SpecScene.Scenes, plan.Segments, segmentID), SegmentID: segmentID, Slot: slot, Plan: spec, Candidates: candidates, Seed: seed, PromptVersion: plan.PromptVersion, ForceRefresh: plan.MediaPlan.ForceRefreshBindings}, planner)
}

func sceneIDForSegment(scenes []scriptpkg.SpecScene, segments []scriptpkg.ScriptSegment, segmentID string) string {
	for _, scene := range scenes {
		if segmentID != "" && scene.SegmentID == segmentID {
			return scene.ID
		}
	}
	// Explicit segments are normalized into scene slots by the stock
	// bindings processor, which runs after this processor. Resolve the
	// scene identity by the canonical segment order instead of falling back
	// to scene-0 while that normalization is still pending.
	for i, segment := range segments {
		if segmentID != "" && segment.ID == segmentID {
			if i < len(scenes) {
				return scenes[i].ID
			}
			// The generated prose may contain fewer scenes than the
			// explicit segment contract. Stock binding normalization
			// creates the missing canonical slot as scene-i later.
			return fmt.Sprintf("scene-%d", i)
		}
	}
	if len(scenes) > 0 {
		return scenes[0].ID
	}
	return ""
}

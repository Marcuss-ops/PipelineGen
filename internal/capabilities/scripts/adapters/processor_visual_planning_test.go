package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type countingVisualResolver struct {
	calls  int
	scenes int
}

func (r *countingVisualResolver) Resolve(_ context.Context, req mediamemory.ResolveRequest) (mediamemory.ResolveResult, error) {
	r.calls++
	r.scenes += len(req.Scenes)
	plans := make([]mediamemory.SceneVisualPlan, 0, len(req.Scenes))
	for _, s := range req.Scenes {
		plans = append(plans, mediamemory.SceneVisualPlan{
			SceneID:    s.ID,
			SegmentID:  s.ID,
			Text:       s.Text,
			Layers:     []mediamemory.Layer{{Slot: mediadomain.SlotPrimaryVideo, AssetID: "resolver-winner", Provider: "drive"}},
			Candidates: []mediamemory.CandidateOption{{AssetID: "resolver-winner", Provider: "drive"}, {AssetID: "other", Provider: "artlist"}},
		})
	}
	return mediamemory.ResolveResult{Plans: plans}, nil
}

type fixedPlanner struct{ id string }

func (p fixedPlanner) Select(context.Context, VisualSelectionRequest) (string, error) {
	return p.id, nil
}

func TestVisualPlanningProcessorResolvesAllScenesInOneBatch(t *testing.T) {
	resolver := &countingVisualResolver{}
	proc := NewVisualPlanningProcessor(resolver, nil, nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "job", Language: "it", MediaPlan: mediadomain.MediaPlanSpec{Mode: mediadomain.MediaPlanModeHybrid}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
		{ID: "s1", SegmentID: "seg-1", Text: "uno"}, {ID: "s2", SegmentID: "seg-2", Text: "due"}, {ID: "s3", SegmentID: "seg-3", Text: "tre"},
	}}}
	result, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || resolver.scenes != 3 {
		t.Fatalf("resolver calls/scenes=%d/%d, want 1/3", resolver.calls, resolver.scenes)
	}
	if len(result.VisualPlans) != 3 {
		t.Fatalf("visual plans=%d, want 3", len(result.VisualPlans))
	}
}

func TestVisualPlanningProcessorPopulatesVisualPlanWithLayersAndColdCandidates(t *testing.T) {
	resolver := &countingVisualResolver{}
	proc := NewVisualPlanningProcessor(resolver, nil, nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "job", Language: "it", MediaPlan: mediadomain.MediaPlanSpec{Mode: mediadomain.MediaPlanModeHybrid}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
		{ID: "s1", SegmentID: "seg-1", Text: "uno"},
	}}}
	result, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SynthesizedScenes) != 1 {
		t.Fatalf("scenes=%d, want 1", len(result.SynthesizedScenes))
	}
	visualPlan := result.SynthesizedScenes[0].VisualPlan
	if visualPlan == nil {
		t.Fatal("expected VisualPlan to be populated")
	}
	if len(visualPlan.Layers) != 1 {
		t.Fatalf("layers=%d, want 1", len(visualPlan.Layers))
	}
	if visualPlan.Layers[0].AssetID != "resolver-winner" {
		t.Fatalf("layer asset_id=%q, want resolver-winner", visualPlan.Layers[0].AssetID)
	}
	if len(visualPlan.Candidates) != 2 {
		t.Fatalf("candidates=%d, want 2", len(visualPlan.Candidates))
	}
	if visualPlan.Candidates[0].AssetID != "resolver-winner" || visualPlan.Candidates[1].AssetID != "other" {
		t.Fatalf("unexpected candidates: %+v", visualPlan.Candidates)
	}
}

func TestVisualPlanningLockedAssignmentSkipsResolverAndPlannerCannotInvent(t *testing.T) {
	resolver := &countingVisualResolver{}
	proc := NewVisualPlanningProcessor(resolver, fixedPlanner{id: "invented"}, nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "job", Language: "it", Segments: []scriptpkg.ScriptSegment{{ID: "seg-1"}, {ID: "seg-2"}}, MediaPlan: mediadomain.MediaPlanSpec{
		Mode:        mediadomain.MediaPlanModeHybrid,
		Assignments: []mediadomain.SegmentMediaAssignment{{SegmentID: "seg-1", Slot: "primary_video", Locked: true, Asset: mediadomain.MediaRef{Kind: "stock", AssetID: "locked-drive", Provider: "drive"}}},
	}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "s1", SegmentID: "seg-1", Text: "locked"}, {ID: "s2", SegmentID: "seg-2", Text: "open"}}}}
	result, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || resolver.scenes != 1 {
		t.Fatalf("resolver calls/scenes=%d/%d, want 1/1", resolver.calls, resolver.scenes)
	}
	if result.VisualPlans[0].Layers[0].AssetID != "locked-drive" {
		t.Fatalf("locked asset=%q", result.VisualPlans[0].Layers[0].AssetID)
	}
	if result.SynthesizedScenes[0].Bindings.Stock == nil || result.SynthesizedScenes[0].Bindings.Stock.AssetID != "locked-drive" {
		t.Fatalf("locked compatibility binding=%+v", result.SynthesizedScenes[0].Bindings.Stock)
	}
	if result.SynthesizedScenes[0].Bindings.Clip == nil || result.SynthesizedScenes[0].Bindings.Clip.ClipID != "locked-drive" {
		t.Fatalf("locked primary-video clip binding=%+v", result.SynthesizedScenes[0].Bindings.Clip)
	}
	if result.VisualPlans[1].Layers[0].AssetID != "resolver-winner" {
		t.Fatalf("invalid planner changed asset=%q", result.VisualPlans[1].Layers[0].AssetID)
	}
}

func TestVisualPlanningArtlistDisabledSkipsImplicitPrimaryVideo(t *testing.T) {
	resolver := &countingVisualResolver{}
	proc := NewVisualPlanningProcessor(resolver, nil, nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "job",
		Language: "en",
		MediaPlan: mediadomain.MediaPlanSpec{
			Mode: mediadomain.MediaPlanModeHybrid,
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist: mediadomain.MediaToggleDisabled,
			},
		},
	}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
		{ID: "s1", SegmentID: "seg-1", Text: "scene"},
	}}}
	result, err := proc.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls=%d, want 0 when Artlist is explicitly disabled", resolver.calls)
	}
	if result.Changed {
		t.Fatalf("result changed unexpectedly: %+v", result)
	}
}

package visual

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

type fakePlanner struct {
	selected []string
	called   int
}

func (p *fakePlanner) Select(context.Context, PlannerRequest) (PlannerResult, error) {
	p.called++
	return PlannerResult{SelectedAssetIDs: append([]string(nil), p.selected...), Reason: "test"}, nil
}

func candidates(ids ...string) []Candidate {
	out := make([]Candidate, len(ids))
	for i, id := range ids {
		out[i] = Candidate{AssetID: id, DurationMs: 1000}
	}
	return out
}

func TestResolveManualIntroPreservesOrderAndSkipsPlanner(t *testing.T) {
	p := &fakePlanner{}
	pos := 2
	got, err := Resolve(context.Background(), Request{SceneID: "scene-1", Slot: media.VisualSlotIntro, Candidates: candidates("a", "b", "c"), Plan: media.VisualSlotPlan{
		Mode:     media.VisualSelectionManual,
		Clips:    []media.VisualClip{{AssetID: "a", Locked: true}, {AssetID: "b", Locked: true}, {AssetID: "c", Locked: true, Position: &pos}},
		MaxClips: 3,
	}}, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 0 {
		t.Fatal("manual mode called planner")
	}
	if len(got.Assignments) != 3 || got.Assignments[0].AssetID != "a" || got.Assignments[1].AssetID != "b" || got.Assignments[2].AssetID != "c" {
		t.Fatalf("assignments=%+v", got.Assignments)
	}
	for _, a := range got.Assignments {
		if !a.Locked || a.SelectedBy != media.VisualSelectedByUser {
			t.Fatalf("manual provenance=%+v", a)
		}
	}
}

func TestResolveHybridKeepsLockedAndGemmaUsesClosedCandidates(t *testing.T) {
	p := &fakePlanner{selected: []string{"c", "not-allowed", "c"}}
	pos := 0
	got, err := Resolve(context.Background(), Request{SegmentID: "seg-1", Slot: media.VisualSlotPostSegment, Candidates: candidates("a", "b", "c"), Plan: media.VisualSlotPlan{
		Mode:              media.VisualSelectionHybrid,
		Clips:             []media.VisualClip{{AssetID: "a", Locked: true, Position: &pos}},
		CandidateAssetIDs: []string{"b", "c"}, MaxClips: 3,
	}}, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("planner calls=%d", p.called)
	}
	if len(got.Assignments) != 3 || got.Assignments[0].AssetID != "a" || got.Assignments[0].Locked == false {
		t.Fatalf("assignments=%+v", got.Assignments)
	}
	if got.Assignments[1].AssetID != "c" || got.Assignments[2].AssetID != "b" {
		t.Fatalf("closed selection/fallback=%+v", got.Assignments)
	}
	for _, a := range got.Assignments[1:] {
		if a.AssetID == "not-allowed" || a.Locked {
			t.Fatalf("bad AI assignment=%+v", a)
		}
	}
}

func TestResolveInvalidGemmaAndSeedFallback(t *testing.T) {
	p := &fakePlanner{selected: []string{"invented", "invented"}}
	req := Request{Slot: media.VisualSlotIntro, Seed: 99, Candidates: candidates("a", "b", "c"), Plan: media.VisualSlotPlan{Mode: media.VisualSelectionGemma, MaxClips: 2, CandidateAssetIDs: []string{"a", "b", "c"}}}
	one, err := Resolve(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Resolve(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Assignments) != 2 || one.Assignments[0].AssetID != two.Assignments[0].AssetID || one.Assignments[1].AssetID != two.Assignments[1].AssetID {
		t.Fatalf("non-deterministic fallback: %+v vs %+v", one.Assignments, two.Assignments)
	}
	for _, a := range one.Assignments {
		if a.SelectedBy != media.VisualSelectedBySampler || a.AssetID == "invented" {
			t.Fatalf("invalid planner escaped: %+v", a)
		}
	}
}

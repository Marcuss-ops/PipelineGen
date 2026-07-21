package planner

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
)

func candidate(id, mediaType string) brain.Candidate {
	return brain.Candidate{
		ID:                   id,
		AssetID:              "asset-" + id,
		MediaType:            mediaType,
		MaterializationState: "required",
		Provider:             "test",
		Score:                0.9,
	}
}

func TestDefaultPlanner_AssignsLayers(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s1",
		DurationMS: 5000,
		Slots:      []brain.SlotKind{brain.SlotPrimaryVideo, brain.SlotSecondaryImage},
	}
	candidates := []brain.Candidate{
		candidate("c1", "video"),
		candidate("c2", "image"),
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(plan.Layers))
	}
	if plan.Layers[0].Slot != brain.SlotPrimaryVideo || plan.Layers[0].CandidateID != "c1" {
		t.Errorf("unexpected layer[0]: %+v", plan.Layers[0])
	}
	if plan.Layers[1].Slot != brain.SlotSecondaryImage || plan.Layers[1].CandidateID != "c2" {
		t.Errorf("unexpected layer[1]: %+v", plan.Layers[1])
	}
	if plan.Status != "success" {
		t.Errorf("expected status success, got %q", plan.Status)
	}
}

func TestDefaultPlanner_PartialWhenFewerCandidates(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s2",
		DurationMS: 3000,
		Slots:      []brain.SlotKind{brain.SlotPrimaryVideo, brain.SlotSecondaryImage},
	}
	candidates := []brain.Candidate{candidate("c1", "video")}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(plan.Layers))
	}
	if plan.Status != "partial" {
		t.Errorf("expected partial status, got %q", plan.Status)
	}
}

func TestDefaultPlanner_EmptyPlanWhenNoCandidates(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:    "s3",
		Slots: []brain.SlotKind{brain.SlotPrimaryVideo},
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 0 {
		t.Errorf("expected 0 layers, got %d", len(plan.Layers))
	}
	if plan.Status != "empty" {
		t.Errorf("expected empty status, got %q", plan.Status)
	}
}

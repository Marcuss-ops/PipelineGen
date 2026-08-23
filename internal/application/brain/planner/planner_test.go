package planner

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
		Slots:      []media.SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage},
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
	if plan.Layers[0].Slot != media.SlotPrimaryVideo || plan.Layers[0].CandidateID != "c1" {
		t.Errorf("unexpected layer[0]: %+v", plan.Layers[0])
	}
	if plan.Layers[1].Slot != media.SlotSecondaryImage || plan.Layers[1].CandidateID != "c2" {
		t.Errorf("unexpected layer[1]: %+v", plan.Layers[1])
	}
	if plan.Status != "success" {
		t.Errorf("expected status success, got %q", plan.Status)
	}
}

func TestSlotCandidateSampler_MediaTypeGate(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s1",
		DurationMS: 5000,
		Slots:      []media.SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage},
	}
	candidates := []brain.Candidate{
		candidate("c1", "image"),
		candidate("c2", "video"),
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(plan.Layers))
	}
	// Even though c1 is ranked first, it is an image and cannot be
	// assigned to the primary_video slot.
	if plan.Layers[0].Slot != media.SlotPrimaryVideo || plan.Layers[0].CandidateID != "c2" {
		t.Errorf("expected video candidate c2 in primary_video, got %+v", plan.Layers[0])
	}
	if plan.Layers[1].Slot != media.SlotSecondaryImage || plan.Layers[1].CandidateID != "c1" {
		t.Errorf("expected image candidate c1 in secondary_image, got %+v", plan.Layers[1])
	}
}

func TestSlotCandidateSampler_Diversity(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s1",
		DurationMS: 5000,
		Slots:      []media.SlotKind{media.SlotSecondaryImage, media.SlotBackground},
	}
	candidates := []brain.Candidate{
		{ID: "c1", AssetID: "asset-a", MediaType: "image", Score: 0.9},
		{ID: "c2", AssetID: "asset-a", MediaType: "image", Score: 0.8},
		{ID: "c3", AssetID: "asset-b", MediaType: "image", Score: 0.7},
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(plan.Layers))
	}
	if plan.Layers[0].CandidateID != "c1" {
		t.Errorf("expected c1 in first slot, got %s", plan.Layers[0].CandidateID)
	}
	// c2 shares asset-a, so it should be skipped in favor of c3.
	if plan.Layers[1].CandidateID != "c3" {
		t.Errorf("expected c3 in second slot due to diversity, got %s", plan.Layers[1].CandidateID)
	}
}

func TestSlotCandidateSampler_DiversityFallback(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s1",
		DurationMS: 5000,
		Slots:      []media.SlotKind{media.SlotSecondaryImage, media.SlotBackground},
	}
	candidates := []brain.Candidate{
		{ID: "c1", AssetID: "asset-a", MediaType: "image", Score: 0.9},
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only one candidate exists, so diversity must fall back to
	// reusing it for both slots.
	if len(plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(plan.Layers))
	}
	if plan.Layers[0].CandidateID != "c1" || plan.Layers[1].CandidateID != "c1" {
		t.Errorf("expected fallback reuse of c1 in both slots, got %+v / %+v", plan.Layers[0], plan.Layers[1])
	}
}

func TestSlotCandidateSampler_GatesRejectRightsAndMaterialization(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s1",
		DurationMS: 5000,
		Slots:      []media.SlotKind{media.SlotPrimaryVideo},
	}
	candidates := []brain.Candidate{
		{ID: "c1", AssetID: "asset-a", MediaType: "video", Score: 0.9, RightsStatus: "expired"},
		{ID: "c2", AssetID: "asset-b", MediaType: "video", Score: 0.8, MaterializationState: "failed"},
		{ID: "c3", AssetID: "asset-c", MediaType: "video", Score: 0.7},
	}

	plan, err := p.Plan(context.Background(), scene, brain.VisualIntent{}, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(plan.Layers))
	}
	if plan.Layers[0].CandidateID != "c3" {
		t.Errorf("expected c3 after gate filtering, got %s", plan.Layers[0].CandidateID)
	}
}

func TestDefaultPlanner_PartialWhenFewerCandidates(t *testing.T) {
	p := NewDefaultPlanner()
	scene := brain.SceneRequest{
		ID:         "s2",
		DurationMS: 3000,
		Slots:      []media.SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage},
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
		Slots: []media.SlotKind{media.SlotPrimaryVideo},
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

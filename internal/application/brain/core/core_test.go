package core

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/ranker"
)

type fakeSearcher struct {
	candidates []brain.Candidate
}

func (f *fakeSearcher) Search(_ context.Context, _ brain.SearchQuery) (brain.SearchResult, error) {
	return brain.SearchResult{Candidates: f.candidates}, nil
}

func newTestBrain(candidates []brain.Candidate) brain.Brain {
	return NewCanonicalBrain(
		normalizer.NewDefaultNormalizer(),
		intent.NewDefaultResolver(),
		&fakeSearcher{candidates: candidates},
		ranker.NewDefaultRanker(),
		planner.NewDefaultPlanner(),
	)
}

func TestCanonicalBrain_ResolvesScene(t *testing.T) {
	candidates := []brain.Candidate{
		{ID: "c1", AssetID: "a1", MediaType: "video", Score: 0.9, Title: "Maya city"},
	}
	b := newTestBrain(candidates)

	req := brain.BrainRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []brain.SceneRequest{
			{
				ID:         "s1",
				Text:       "I Maya costruirono città",
				DurationMS: 5000,
				Slots:      []brain.SlotKind{brain.SlotPrimaryVideo},
			},
		},
		Policy: brain.ResolutionPolicy{MaxCandidatesPerSlot: 10},
	}

	result, err := b.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(result.Scenes))
	}
	if len(result.Scenes[0].Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(result.Scenes[0].Layers))
	}
	if result.Scenes[0].Layers[0].CandidateID != "c1" {
		t.Errorf("unexpected candidate: %s", result.Scenes[0].Layers[0].CandidateID)
	}
}

func TestCanonicalBrain_EmptySearchResult(t *testing.T) {
	b := newTestBrain(nil)

	req := brain.BrainRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []brain.SceneRequest{
			{
				ID:         "s1",
				Text:       "I Maya guardavano le stelle",
				DurationMS: 3000,
				Slots:      []brain.SlotKind{brain.SlotPrimaryVideo},
			},
		},
		Policy: brain.ResolutionPolicy{MaxCandidatesPerSlot: 10},
	}

	result, err := b.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(result.Scenes))
	}
	if result.Scenes[0].Status != "empty" {
		t.Errorf("expected empty status, got %q", result.Scenes[0].Status)
	}
}

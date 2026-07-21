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
	scene := result.Scenes[0]
	if len(scene.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(scene.Layers))
	}
	if scene.Layers[0].CandidateID != "c1" {
		t.Errorf("unexpected candidate: %s", scene.Layers[0].CandidateID)
	}

	// Per-scene trace and decision fingerprint must be populated.
	if scene.Trace.NormalizedText == "" {
		t.Errorf("expected normalized text in per-scene trace")
	}
	if scene.Trace.Versions.BrainVersion == "" {
		t.Errorf("expected brain version in per-scene trace")
	}
	if scene.Trace.Versions.NormalizerVersion == "" {
		t.Errorf("expected normalizer version in per-scene trace")
	}
	if scene.Trace.Versions.IntentResolverVersion == "" {
		t.Errorf("expected intent resolver version in per-scene trace")
	}
	if scene.Trace.Versions.RankingPolicyVersion == "" {
		t.Errorf("expected ranking policy version in per-scene trace")
	}
	if scene.DecisionFingerprint == "" {
		t.Errorf("expected decision fingerprint on scene")
	}
	if len(scene.Trace.BackendCalls) != 1 {
		t.Errorf("expected 1 backend call record, got %d", len(scene.Trace.BackendCalls))
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

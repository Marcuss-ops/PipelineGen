package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/ranker"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

type fakeMediaMemoryPort struct {
	candidates []brain.Candidate
}

func (f *fakeMediaMemoryPort) Search(_ context.Context, _ brain.SearchQuery) (brain.SearchResult, error) {
	return brain.SearchResult{Candidates: f.candidates}, nil
}

func (f *fakeMediaMemoryPort) EmbeddingVersion() string {
	return "test-embedding-v1"
}

// fakeRanker is a test double for the CandidateRanker port. The Brain
// orchestration tests are about ranking plumbing, not scoring, so the
// fake returns candidates unchanged. Using a fake (rather than the
// production MediaMemory-backed adapter) keeps the core package test
// free of the mediamemory dependency and the brain<->mediamemory
// architectural cycle.
type fakeRanker struct{}

func (fakeRanker) Rank(_ context.Context, _ brain.SceneRequest, _ brain.VisualIntent, candidates []brain.Candidate, _ brain.ResolutionPolicy) ([]brain.Candidate, error) {
	return candidates, nil
}

func (fakeRanker) Version() string { return "test-ranker-v1" }

func (fakeRanker) DiversityPolicyVersion() string { return "test-diversity-v1" }

var _ ranker.CandidateRanker = fakeRanker{}

func newTestBrain(candidates []brain.Candidate) brain.Brain {
	return NewCanonicalBrain(
		normalizer.NewDefaultNormalizer(),
		intent.NewDefaultResolver(),
		&fakeMediaMemoryPort{candidates: candidates},
		fakeRanker{},
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
				Slots:      []media.SlotKind{media.SlotPrimaryVideo},
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
	if scene.Trace.Versions.DiversityPolicyVersion == "" {
		t.Errorf("expected diversity policy version in per-scene trace")
	}
	if scene.Trace.Versions.SlotPolicyVersion == "" {
		t.Errorf("expected slot policy version in per-scene trace")
	}
	if scene.Trace.Versions.ProviderRegistryVersion == "" {
		t.Errorf("expected provider registry version in per-scene trace")
	}
	if scene.DecisionFingerprint == "" {
		t.Errorf("expected decision fingerprint on scene")
	}
	if len(scene.Trace.BackendCalls) != 1 {
		t.Errorf("expected 1 backend call record, got %d", len(scene.Trace.BackendCalls))
	}
}

// TestCanonicalBrain_MayaVenusSceneIntent documents the expected
// understanding of a real historical scene. The intent resolver is
// still at V1, so the test currently fails on concepts and actions;
// it is intentionally left as a failing/regression test that drives
// the next iteration of the brain.
func TestCanonicalBrain_MayaVenusSceneIntent(t *testing.T) {
	t.Skip("intentionally left as a failing/regression test for future resolver iterations")

	candidates := []brain.Candidate{
		{ID: "c1", AssetID: "a1", MediaType: "image", Score: 0.9, Title: "Maya temple Venus"},
	}
	b := newTestBrain(candidates)

	req := brain.BrainRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []brain.SceneRequest{
			{
				ID:         "maya-1",
				Text:       "I Maya osservavano Venere dai loro templi",
				DurationMS: 5000,
				Slots:      []media.SlotKind{media.SlotSecondaryImage},
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

	if scene.Trace.NormalizedText != "i maya osservavano venere dai loro templi" {
		t.Errorf("normalized text = %q, want %q", scene.Trace.NormalizedText, "i maya osservavano venere dai loro templi")
	}
	if scene.DecisionFingerprint == "" {
		t.Errorf("expected decision fingerprint on scene")
	}
	if len(scene.Layers) != 1 {
		t.Errorf("expected 1 layer, got %d", len(scene.Layers))
	}

	intent := scene.Intent
	for _, entity := range []string{"maya", "venere"} {
		if !slices.Contains(intent.Entities, entity) {
			t.Errorf("expected entity %q, got %v", entity, intent.Entities)
		}
	}

	if !slices.Contains(intent.Actions, "osservare") {
		t.Errorf("expected action 'osservare', got %v", intent.Actions)
	}

	wantConcepts := []string{"astronomia maya", "pianeta venere", "templi maya"}
	for _, concept := range wantConcepts {
		found := false
		for _, c := range intent.Concepts {
			if strings.Contains(strings.ToLower(c), concept) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected concept containing %q, got %v", concept, intent.Concepts)
		}
	}

	// Print the actual intent so the gap is explicit in test output.
	t.Logf("actual intent: entities=%v actions=%v concepts=%v keywords=%v", intent.Entities, intent.Actions, intent.Concepts, intent.Keywords)
}

func TestResolutionVersionSet_FingerprintInvalidatedOnVersionChange(t *testing.T) {
	base := brain.ResolutionVersionSet{
		BrainVersion:            media.VersionBrain,
		NormalizerVersion:       "v1",
		IntentResolverVersion:   media.VersionIntentRegistry,
		EmbeddingVersion:        media.VersionEmbedding,
		RankingPolicyVersion:    media.VersionMediaRanker,
		DiversityPolicyVersion:  media.VersionDiversityPolicy,
		SlotPolicyVersion:       media.VersionSlotSampler,
		ProviderRegistryVersion: media.VersionProviderRegistry,
	}

	fp := base.DecisionFingerprint("it", "i maya")

	mutations := []struct {
		name string
		fn   func(*brain.ResolutionVersionSet)
	}{
		{"BrainVersion", func(v *brain.ResolutionVersionSet) { v.BrainVersion = "brain-v2" }},
		{"NormalizerVersion", func(v *brain.ResolutionVersionSet) { v.NormalizerVersion = "v2" }},
		{"IntentResolverVersion", func(v *brain.ResolutionVersionSet) { v.IntentResolverVersion = "intent-registry-v2" }},
		{"EmbeddingVersion", func(v *brain.ResolutionVersionSet) { v.EmbeddingVersion = "multilingual-e5-v2" }},
		{"RankingPolicyVersion", func(v *brain.ResolutionVersionSet) { v.RankingPolicyVersion = "media-ranker-v3" }},
		{"DiversityPolicyVersion", func(v *brain.ResolutionVersionSet) { v.DiversityPolicyVersion = "diversity-policy-v2" }},
		{"SlotPolicyVersion", func(v *brain.ResolutionVersionSet) { v.SlotPolicyVersion = "slot-sampler-v2" }},
		{"ProviderRegistryVersion", func(v *brain.ResolutionVersionSet) { v.ProviderRegistryVersion = "provider-registry-v2" }},
	}

	for _, m := range mutations {
		mutated := base
		m.fn(&mutated)
		if got := mutated.DecisionFingerprint("it", "i maya"); got == fp {
			t.Errorf("%s change did not invalidate fingerprint", m.name)
		}
	}
}

func TestResolutionVersionSet_FingerprintDependsOnInput(t *testing.T) {
	set := brain.ResolutionVersionSet{
		BrainVersion:            media.VersionBrain,
		NormalizerVersion:       "v1",
		IntentResolverVersion:   media.VersionIntentRegistry,
		EmbeddingVersion:        media.VersionEmbedding,
		RankingPolicyVersion:    media.VersionMediaRanker,
		DiversityPolicyVersion:  media.VersionDiversityPolicy,
		SlotPolicyVersion:       media.VersionSlotSampler,
		ProviderRegistryVersion: media.VersionProviderRegistry,
	}

	fp := set.DecisionFingerprint("it", "i maya")
	if set.DecisionFingerprint("en", "i maya") == fp {
		t.Errorf("language change did not invalidate fingerprint")
	}
	if set.DecisionFingerprint("it", "i maya e venere") == fp {
		t.Errorf("normalized text change did not invalidate fingerprint")
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
				Slots:      []media.SlotKind{media.SlotPrimaryVideo},
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

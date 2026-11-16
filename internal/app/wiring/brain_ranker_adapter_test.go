package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func mmRankCandidate(id string, score float64, mediaType string) brain.Candidate {
	return brain.Candidate{
		ID:         id,
		AssetID:    "asset-" + id,
		Score:      score,
		MediaType:  mediaType,
		Title:      id,
		DurationMs: 4000,
	}
}

// stubPoolRanker records the request and answers through respond, so the
// adapter's contract — one pool, the caller's bonus, the ranker's order — is
// tested without a worker process.
type stubPoolRanker struct {
	response func(mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error)
	request  mediamemory.PoolRankRequest
	calls    int
}

func (s *stubPoolRanker) RankPool(_ context.Context, request mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error) {
	s.request = request
	s.calls++
	if s.response == nil {
		return mediamemory.PoolRankOutcome{}, nil
	}
	return s.response(request)
}

func TestMediaMemoryRankerAdapter_SendsOnePoolWithNeutralSlot(t *testing.T) {
	stub := &stubPoolRanker{}
	r := NewMediaMemoryRankerAdapter(stub)
	candidates := []brain.Candidate{
		mmRankCandidate("c-low", 0.3, "video"),
		mmRankCandidate("c-high", 0.9, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", DurationMS: 7000, Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	policy := brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}

	if _, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates, policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("adapter called the ranker %d times, want exactly 1", stub.calls)
	}
	if stub.request.SlotKind != brainPoolSlotKind {
		t.Errorf("slot_kind = %q, want neutral %q", stub.request.SlotKind, brainPoolSlotKind)
	}
	if stub.request.SceneDurationMS != 7000 {
		t.Errorf("scene duration = %d, want 7000", stub.request.SceneDurationMS)
	}
	if len(stub.request.Candidates) != len(candidates) {
		t.Fatalf("pool carries %d candidates, want %d", len(stub.request.Candidates), len(candidates))
	}
	seen := map[string]bool{}
	for _, candidate := range stub.request.Candidates {
		if candidate.Bonus != 0.10 {
			t.Errorf("video candidate %q bonus = %v, want 0.10", candidate.Key, candidate.Bonus)
		}
		if candidate.Key == "" || seen[candidate.Key] {
			t.Errorf("pool key %q is missing or repeated", candidate.Key)
		}
		seen[candidate.Key] = true
	}
}

func TestMediaMemoryRankerAdapter_OffSlotCandidateCarriesNoBonus(t *testing.T) {
	stub := &stubPoolRanker{}
	r := NewMediaMemoryRankerAdapter(stub)

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	if _, err := r.Rank(context.Background(), scene, brain.VisualIntent{},
		[]brain.Candidate{mmRankCandidate("img", 0.85, "image")},
		brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.request.Candidates[0].Bonus != 0 {
		t.Errorf("image candidate under primary_video bonus = %v, want 0",
			stub.request.Candidates[0].Bonus)
	}
}

func TestMediaMemoryRankerAdapter_ReturnsCandidatesInRankerOrderAndRespectsLimit(t *testing.T) {
	stub := &stubPoolRanker{response: func(request mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error) {
		// Answer in reverse pool order: the adapter must follow this order
		// and must not re-sort it.
		ranked := make([]mediamemory.PoolRankScore, 0, len(request.Candidates))
		for index := len(request.Candidates) - 1; index >= 0; index-- {
			candidate := request.Candidates[index]
			ranked = append(ranked, mediamemory.PoolRankScore{
				CandidateID: candidate.Key,
				AssetID:     candidate.Filtered.Candidate.AssetID,
			})
		}
		return mediamemory.PoolRankOutcome{Ranked: ranked}, nil
	}}
	r := NewMediaMemoryRankerAdapter(stub)
	candidates := []brain.Candidate{
		mmRankCandidate("a", 0.9, "video"),
		mmRankCandidate("b", 0.8, "video"),
		mmRankCandidate("c", 0.7, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	ranked, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates,
		brain.ResolutionPolicy{MaxCandidatesPerSlot: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(ranked))
	}
	if ranked[0].ID != "c" || ranked[1].ID != "b" {
		t.Fatalf("adapter did not follow the ranker order: %v", []string{ranked[0].ID, ranked[1].ID})
	}
	if len(stub.request.Candidates) != 3 {
		t.Fatalf("pool carries %d candidates, want 3", len(stub.request.Candidates))
	}
}

func TestMediaMemoryRankerAdapter_FailsClosedOnRankerError(t *testing.T) {
	stub := &stubPoolRanker{response: func(mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error) {
		return mediamemory.PoolRankOutcome{}, errors.New("worker unavailable")
	}}
	r := NewMediaMemoryRankerAdapter(stub)

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	if _, err := r.Rank(context.Background(), scene, brain.VisualIntent{},
		[]brain.Candidate{mmRankCandidate("a", 0.9, "video")},
		brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}); err == nil {
		t.Fatal("a ranker failure must surface as an error, not an empty pool")
	}
}

func TestMediaMemoryRankerAdapter_FailsClosedOnUnknownCandidate(t *testing.T) {
	stub := &stubPoolRanker{response: func(mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error) {
		return mediamemory.PoolRankOutcome{
			Ranked: []mediamemory.PoolRankScore{{CandidateID: "not-in-the-pool", AssetID: "ghost"}},
		}, nil
	}}
	r := NewMediaMemoryRankerAdapter(stub)

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	if _, err := r.Rank(context.Background(), scene, brain.VisualIntent{},
		[]brain.Candidate{mmRankCandidate("a", 0.9, "video")},
		brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}); err == nil {
		t.Fatal("an answer naming a candidate outside the pool must fail closed")
	}
}

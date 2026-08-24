package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func mmRankCandidate(id string, score float64, mediaType string) brain.Candidate {
	return brain.Candidate{
		ID:        id,
		AssetID:   "asset-" + id,
		Score:     score,
		MediaType: mediaType,
		Title:     id,
	}
}

func TestMediaMemoryRankerAdapter_OrdersByScore(t *testing.T) {
	r := NewMediaMemoryRankerAdapter(mediamemory.NewDefaultRanker(nil, nil))
	candidates := []brain.Candidate{
		mmRankCandidate("c-low", 0.3, "video"),
		mmRankCandidate("c-high", 0.9, "video"),
		mmRankCandidate("c-mid", 0.6, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	policy := brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}

	ranked, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ranked) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(ranked))
	}
	if ranked[0].ID != "c-high" || ranked[1].ID != "c-mid" || ranked[2].ID != "c-low" {
		t.Errorf("unexpected order: %v", mmRankCandidateIDs(ranked))
	}
}

func TestMediaMemoryRankerAdapter_RespectsLimit(t *testing.T) {
	r := NewMediaMemoryRankerAdapter(mediamemory.NewDefaultRanker(nil, nil))
	candidates := []brain.Candidate{
		mmRankCandidate("a", 0.9, "video"),
		mmRankCandidate("b", 0.8, "video"),
		mmRankCandidate("c", 0.7, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	policy := brain.ResolutionPolicy{MaxCandidatesPerSlot: 2}

	ranked, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ranked) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(ranked))
	}
}

func TestMediaMemoryRankerAdapter_MediaTypeBonus(t *testing.T) {
	r := NewMediaMemoryRankerAdapter(mediamemory.NewDefaultRanker(nil, nil))
	candidates := []brain.Candidate{
		mmRankCandidate("img", 0.85, "image"),
		mmRankCandidate("vid", 0.80, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	policy := brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}

	ranked, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Video candidate should win despite lower base score thanks to
	// the slot-fitness bonus.
	if ranked[0].ID != "vid" {
		t.Errorf("expected video candidate to win, got %v", mmRankCandidateIDs(ranked))
	}
}

func mmRankCandidateIDs(in []brain.Candidate) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].ID
	}
	return out
}

func TestMediaMemoryRankerAdapter_TieBreakByID(t *testing.T) {
	r := NewMediaMemoryRankerAdapter(mediamemory.NewDefaultRanker(nil, nil))
	candidates := []brain.Candidate{
		mmRankCandidate("b", 0.5, "video"),
		mmRankCandidate("a", 0.5, "video"),
	}

	scene := brain.SceneRequest{ID: "s1", Slots: []media.SlotKind{media.SlotPrimaryVideo}}
	policy := brain.ResolutionPolicy{MaxCandidatesPerSlot: 10}

	ranked, err := r.Rank(context.Background(), scene, brain.VisualIntent{}, candidates, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ranked[0].ID != "a" {
		t.Errorf("expected ID tie-break to order ascending, got %v", mmRankCandidateIDs(ranked))
	}
}

package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestResolveSegmentTimingBudget_Priority(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-1",
		Text:      "one two three four five",
		Assets: scriptpkg.SegmentAssetSelection{
			PrimaryVideo: &scriptpkg.SegmentAssetCandidate{DurationMs: 9000},
			Candidates:   []scriptpkg.SegmentAssetCandidate{{Provider: scriptpkg.VidRushProviderArtlist, DurationMs: 7000}},
		},
	}
	budget := ResolveSegmentTimingBudget(segment, nil)
	if budget.DurationMs != 9000 || budget.Source != "voiceover" {
		t.Fatalf("budget = %#v, want voiceover 9000ms", budget)
	}
}

func TestResolveSegmentTimingBudget_FallsBackToScene(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-1",
		Text:      "one two",
		Assets: scriptpkg.SegmentAssetSelection{
			Candidates: []scriptpkg.SegmentAssetCandidate{{Provider: scriptpkg.VidRushProviderArtlist, DurationMs: 7000}},
		},
	}
	budget := ResolveSegmentTimingBudget(segment, nil)
	if budget.DurationMs != 7000 || budget.Source != "scene" {
		t.Fatalf("budget = %#v, want scene 7000ms", budget)
	}
}

func TestResolveSegmentTimingBudget_FallsBackToTextEstimate(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-1", Text: "one two three"}
	budget := ResolveSegmentTimingBudget(segment, nil)
	if budget.DurationMs != 4000 || budget.Source != "estimated" {
		t.Fatalf("budget = %#v, want estimated 4000ms, got %#v", budget, budget)
	}
}

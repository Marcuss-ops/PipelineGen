package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func semanticGateCandidate(assetID string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID: assetID, Provider: scriptpkg.VidRushProviderInternetImages,
		SourceURL: "https://images.example/" + assetID + ".jpg",
	}
}

func TestApplyInternetImageSemanticGateFailOpenAndThreshold(t *testing.T) {
	unknown := semanticGateCandidate("unknown")
	accepted := semanticGateCandidate("accepted")
	accepted.SemanticStatus = entitycatalog.CandidateSemanticAccepted
	accepted.SemanticScore = 0.9
	atFloor := semanticGateCandidate("at-floor")
	atFloor.SemanticScore = MinInternetImageSemanticScore
	rejected := semanticGateCandidate("rejected")
	rejected.SemanticStatus = entitycatalog.CandidateSemanticRejected
	belowFloor := semanticGateCandidate("below-floor")
	belowFloor.SemanticScore = 0.1

	out := applyInternetImageSemanticGate([]scriptpkg.SegmentAssetCandidate{unknown, accepted, atFloor, rejected, belowFloor})
	got := make([]string, 0, len(out))
	for _, candidate := range out {
		got = append(got, candidate.AssetID)
	}
	want := []string{"unknown", "accepted", "at-floor"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("gate kept %v, want %v", got, want)
	}
}

func TestFilterInternetImageCandidatesAppliesSemanticGate(t *testing.T) {
	rejected := semanticGateCandidate("rejected")
	rejected.SemanticStatus = entitycatalog.CandidateSemanticRejected
	kept := semanticGateCandidate("kept")
	out := filterInternetImageCandidates([]scriptpkg.SegmentAssetCandidate{rejected, kept})
	if len(out) != 1 || out[0].AssetID != "kept" {
		t.Fatalf("filterInternetImageCandidates = %+v, want only the unrejected candidate", out)
	}
}

type erroringSemanticScorer struct{}

func (erroringSemanticScorer) ScoreImageCandidates(context.Context, string, []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, errors.New("sidecar unavailable")
}

type rejectingSemanticScorer struct{}

func (rejectingSemanticScorer) ScoreImageCandidates(_ context.Context, _ string, in []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if candidate.AssetID == "score-reject" {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.SemanticScore = 0
		} else {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticAccepted
			candidate.SemanticScore = 0.8
		}
		out = append(out, candidate)
	}
	return out, nil
}

func TestScoreInternetImageCandidatesFailOpen(t *testing.T) {
	in := []scriptpkg.SegmentAssetCandidate{semanticGateCandidate("a")}
	if out, err := scoreInternetImageCandidates(context.Background(), nil, "q", in); err != nil || len(out) != 1 {
		t.Fatalf("nil scorer = (%v, %v), want passthrough", out, err)
	}
	out, err := scoreInternetImageCandidates(context.Background(), erroringSemanticScorer{}, "q", in)
	if err == nil {
		t.Fatalf("erroring scorer returned no error")
	}
	if len(out) != 1 || out[0].AssetID != "a" {
		t.Fatalf("erroring scorer did not return the unscored candidates: %+v", out)
	}
	scored, err := scoreInternetImageCandidates(context.Background(), rejectingSemanticScorer{}, "q", in)
	if err != nil || scored[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted {
		t.Fatalf("scoring scorer = (%+v, %v)", scored, err)
	}
}

type fixedInternetImageSearcher struct {
	candidates []scriptpkg.SegmentAssetCandidate
}

func (s fixedInternetImageSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return s.candidates, nil
}

func semanticStageInput() ProcessInput {
	return ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "semantic-stage", TextHash: "semantic-stage-hash",
		Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"great barrier reef"}},
	}}}
}

func semanticStagePlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: true,
		MediaPlan: media.MediaPlanSpec{
			ForceRefreshAssets: true,
			ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
}

func TestMediaResolverImageStageSemanticScorerDropsRejectedCandidates(t *testing.T) {
	searcher := fixedInternetImageSearcher{candidates: []scriptpkg.SegmentAssetCandidate{
		semanticGateCandidate("score-keep"),
		semanticGateCandidate("score-reject"),
	}}
	processor := NewMediaResolverImageStage(searcher).WithSemanticScorer(rejectingSemanticScorer{})

	result, err := processor.Process(context.Background(), semanticStagePlan(), semanticStageInput())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("segments = %d, want 1", len(result.VidRushSegments))
	}
	candidates := result.VidRushSegments[0].Assets.Candidates
	if len(candidates) != 1 || candidates[0].AssetID != "score-keep" {
		t.Fatalf("candidates = %+v, want only score-keep", candidates)
	}
	if candidates[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted {
		t.Fatalf("semantic status = %q, want accepted", candidates[0].SemanticStatus)
	}
}

func TestMediaResolverImageStageSemanticScorerErrorKeepsDiscovery(t *testing.T) {
	searcher := fixedInternetImageSearcher{candidates: []scriptpkg.SegmentAssetCandidate{
		semanticGateCandidate("score-keep"),
		semanticGateCandidate("score-reject"),
	}}
	processor := NewMediaResolverImageStage(searcher).WithSemanticScorer(erroringSemanticScorer{})

	result, err := processor.Process(context.Background(), semanticStagePlan(), semanticStageInput())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := len(result.VidRushSegments[0].Assets.Candidates); got != 2 {
		t.Fatalf("candidates = %d, want both kept when scoring is unavailable", got)
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "semantic scoring unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want a semantic-scoring warning", result.Warnings)
	}
}

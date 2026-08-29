package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func selectVidRushPrimaryVideoWithPolicy(candidates []scriptpkg.SegmentAssetCandidate, plan *scriptpkg.ResolvedGenerationPlan, profile scriptpkg.SegmentSemanticProfile, targetDurationMs int64, reranker scriptports.CandidateReranker, ctx context.Context) *scriptpkg.SegmentAssetCandidate {
	eligible := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderArtlist && candidate.Provider != scriptpkg.VidRushProviderYouTube {
			continue
		}
		if !readyVidRushCandidate(candidate) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return nil
	}
	limit := 8
	if plan != nil && plan.MediaPlan.Planner.CandidateLimit > 0 && plan.MediaPlan.Planner.CandidateLimit < limit {
		limit = plan.MediaPlan.Planner.CandidateLimit
	}
	// The common planner is selected by MediaPlannerPolicy. Unknown or empty
	// strategies safely use deterministic ranking; the optional reranker only
	// contributes SemanticScore and never changes candidate identity/timing.
	strategy := "deterministic"
	if plan != nil && strings.TrimSpace(plan.MediaPlan.Planner.Strategy) != "" {
		strategy = strings.ToLower(strings.TrimSpace(plan.MediaPlan.Planner.Strategy))
	}
	if strategy == "small_model_rerank" {
		eligible = NewVidRushWindowRanker().RankWithOptionalReranker(ctx, reranker, eligible, profile, targetDurationMs)
	} else {
		eligible = NewVidRushWindowRanker().Rank(eligible, profile, targetDurationMs)
	}
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	return selectVidRushPrimaryVideo(eligible)
}

func selectVidRushPrimaryVideo(candidates []scriptpkg.SegmentAssetCandidate) *scriptpkg.SegmentAssetCandidate {
	var selected *scriptpkg.SegmentAssetCandidate
	for i := range candidates {
		candidate := candidates[i]
		if (candidate.Provider != scriptpkg.VidRushProviderArtlist && candidate.Provider != scriptpkg.VidRushProviderYouTube) || !readyVidRushCandidate(candidate) {
			continue
		}
		if selected == nil || compareVidRushPrimaryCandidates(candidate, *selected) > 0 {
			copy := candidate
			copy.Score = ScoreVidRushCandidate(candidate, false)
			copy.SelectionReason = "highest ranked verified and persisted video"
			selected = &copy
		}
	}
	return selected
}

func vidRushArtlistDiagnostics(candidates []scriptpkg.SegmentAssetCandidate) []string {
	diagnostics := make([]string, 0, 3)
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderArtlist {
			continue
		}
		diagnostics = append(diagnostics, fmt.Sprintf("asset=%s acquire=%s verify=%s persist=%s source=%t page=%t drive=%t", candidate.AssetID, candidate.AcquisitionStatus, candidate.VerificationStatus, candidate.PersistenceStatus, hasValue(candidate.SourceURL), hasValue(candidate.SourcePageURL), hasValue(candidate.DriveLink)))
		if len(diagnostics) == 3 {
			break
		}
	}
	return diagnostics
}

func hasValue(value string) bool {
	return strings.TrimSpace(value) != ""
}

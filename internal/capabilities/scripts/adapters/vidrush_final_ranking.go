package adapters

import (
	"context"
	"math"
	"sort"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type FinalScoreWeights struct{ Semantic, Transcript, Visual, Technical, DurationFit, ProviderTrust float64 }

var defaultFinalScoreWeights = FinalScoreWeights{Semantic: .40, Transcript: .25, Visual: .15, Technical: .10, DurationFit: .05, ProviderTrust: .05}

type FinalScoreComponents struct{ Semantic, Transcript, Visual, Technical, DurationFit, ProviderTrust float64 }

func FinalScoreWeightsFor() FinalScoreWeights { return defaultFinalScoreWeights }
func clampUnit(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func durationFitScore(d, t int64) float64 {
	if t <= 0 {
		return 1
	}
	if d <= 0 {
		return 0
	}
	r := float64(d) / float64(t)
	if r > 2 {
		r = 2
	}
	return clampUnit(1 - math.Abs(r-1))
}
func providerTrustScore(c scriptpkg.SegmentAssetCandidate) float64 {
	trust := .5
	if strings.EqualFold(strings.TrimSpace(c.RightsStatus), "verified") {
		trust = .9
	}
	if c.ProviderReliability > 0 {
		trust = .6*trust + .4*clampUnit(c.ProviderReliability)
	}
	return trust
}
func FinalScore(c FinalScoreComponents) float64 {
	w := defaultFinalScoreWeights
	return clampUnit(w.Semantic*clampUnit(c.Semantic) + w.Transcript*clampUnit(c.Transcript) + w.Visual*clampUnit(c.Visual) + w.Technical*clampUnit(c.Technical) + w.DurationFit*clampUnit(c.DurationFit) + w.ProviderTrust*clampUnit(c.ProviderTrust))
}

type VidRushWindowRanker struct{}

func NewVidRushWindowRanker() VidRushWindowRanker { return VidRushWindowRanker{} }
func (VidRushWindowRanker) Rank(candidates []scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile, targetMs int64) []scriptpkg.SegmentAssetCandidate {
	return rankWindows(candidates, profile, targetMs)
}

// RankWithOptionalReranker invokes the Small LLM only for semantic scores.
// All non-semantic components, final weighting and ordering remain local and
// deterministic. Reranker errors are a safe deterministic fallback.
func (r VidRushWindowRanker) RankWithOptionalReranker(ctx context.Context, reranker scriptports.CandidateReranker, candidates []scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile, targetMs int64) []scriptpkg.SegmentAssetCandidate {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	if reranker != nil {
		results, err := reranker.Rerank(ctx, scriptports.CandidateRerankRequest{SegmentID: profile.SegmentID, Text: profile.Topic, Topic: profile.Topic, TargetDurationMs: targetMs, Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), out...)})
		if err == nil {
			byID := map[string]scriptports.CandidateRerankResult{}
			for _, result := range results {
				if result.SemanticScore >= 0 && result.SemanticScore <= 1 {
					byID[result.CandidateID] = result
				}
			}
			for i := range out {
				if result, ok := byID[out[i].AssetID]; ok {
					out[i].SemanticScore = result.SemanticScore
				}
			}
		}
	}
	return rankWindows(out, profile, targetMs)
}
func rankWindows(candidates []scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile, targetMs int64) []scriptpkg.SegmentAssetCandidate {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	for i := range out {
		c := &out[i]
		semantic := c.SemanticScore
		if semantic == 0 {
			semantic = profileSemanticMatch(*c, profile)
		}
		c.SemanticScore = semantic
		c.Score = FinalScore(FinalScoreComponents{Semantic: semantic, Transcript: c.RelevanceScore, Visual: profileSemanticMatch(*c, profile), Technical: c.TechnicalQualityScore, DurationFit: durationFitScore(c.DurationMs, targetMs), ProviderTrust: providerTrustScore(*c)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].AssetID < out[j].AssetID
	})
	return out
}

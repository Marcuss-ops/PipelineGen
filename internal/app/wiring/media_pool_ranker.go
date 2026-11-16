// media_pool_ranker.go is the single bridge between mediamemory.PoolRanker and
// the mediasampler worker. It lives in the composition root because it is the
// one place that may depend on both the capability's port and the platform
// transport.
//
// godlike/06 SSOT: this bridge owns the wire mapping and nothing else. It does
// not gate, score, re-sort or repair an answer — the ranker in
// `mediasampler::rank` is the only authority, and rustexec's adapter rejects an
// answer that is not a ranking of the request.
package wiring

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// defaultMediaSamplerBinary is the same default as
// config.External.RustMediaSamplerPath; kept here so the resolver wiring does
// not have to thread a config value through two composition sites.
const defaultMediaSamplerBinary = "bin/mediasampler"

// mediaSamplerBinaryPath resolves the worker path from the environment, with
// the documented default as the fallback.
func mediaSamplerBinaryPath() string {
	if path := strings.TrimSpace(os.Getenv("VELOX_RUST_MEDIASAMPLER_PATH")); path != "" {
		return path
	}
	return defaultMediaSamplerBinary
}

// mediaPoolRankerBridge adapts the worker to mediamemory.PoolRanker.
type mediaPoolRankerBridge struct {
	adapter *rustexec.MediaRankerAdapter
}

var _ mediamemory.PoolRanker = (*mediaPoolRankerBridge)(nil)

// newMediaPoolRankerBridge builds the production ranker. A missing worker is a
// configuration error at boot: a resolver whose only ranker cannot start must
// not be handed out, because every slot would fail closed at request time.
func newMediaPoolRankerBridge(binaryPath string, log *zap.Logger) (mediamemory.PoolRanker, error) {
	if strings.TrimSpace(binaryPath) == "" {
		binaryPath = defaultMediaSamplerBinary
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return nil, fmt.Errorf("media ranker: mediasampler worker %q is not available: %w", binaryPath, err)
	}
	executor := rustexec.NewExecutor(binaryPath, "", log)
	adapter, err := rustexec.NewMediaRankerAdapter(executor)
	if err != nil {
		return nil, fmt.Errorf("media ranker: %w", err)
	}
	return &mediaPoolRankerBridge{adapter: adapter}, nil
}

// RankPool projects one slot pool onto the worker and maps the recorded
// ranking back onto the caller's pool keys.
func (b *mediaPoolRankerBridge) RankPool(ctx context.Context, request mediamemory.PoolRankRequest) (mediamemory.PoolRankOutcome, error) {
	wire := rustexec.MediaRankerRequest{
		SceneDurationMS: request.SceneDurationMS,
		PrevVideoID:     request.PrevVideoID,
		SlotKind:        string(request.SlotKind),
		Candidates:      make([]rustexec.MediaRankerCandidate, 0, len(request.Candidates)),
	}
	for _, candidate := range request.Candidates {
		wire.Candidates = append(wire.Candidates, toWireCandidate(candidate))
	}

	answer, err := b.adapter.RankCandidates(ctx, wire)
	if err != nil {
		return mediamemory.PoolRankOutcome{}, err
	}

	outcome := mediamemory.PoolRankOutcome{
		Ranked: make([]mediamemory.PoolRankScore, 0, len(answer.Ranked)),
		Drops:  make([]mediamemory.PoolRankDrop, 0, len(answer.Drops)),
	}
	for _, ranked := range answer.Ranked {
		outcome.Ranked = append(outcome.Ranked, mediamemory.PoolRankScore{
			CandidateID: ranked.CandidateID,
			AssetID:     ranked.AssetID,
			FinalScore:  ranked.FinalScore,
			Verdict:     mediamemory.RankingVerdict(ranked.Verdict),
			Reason:      ranked.Reason,
		})
	}
	for _, drop := range answer.Drops {
		outcome.Drops = append(outcome.Drops, mediamemory.PoolRankDrop{
			CandidateID: drop.CandidateID,
			AssetID:     drop.AssetID,
			Gate:        drop.Gate,
			Reason:      drop.Reason,
		})
	}
	if answer.Winner != nil {
		outcome.Winner = &mediamemory.PoolRankScore{
			CandidateID: answer.Winner.CandidateID,
			AssetID:     answer.Winner.AssetID,
			FinalScore:  answer.Winner.FinalScore,
			Verdict:     mediamemory.RankingVerdict(answer.Winner.Verdict),
			Reason:      answer.Winner.Reason,
		}
	}
	return outcome, nil
}

// toWireCandidate mirrors the fields the ranker reads. The binding envelope is
// only sent when it carries an asset, because the ranker branches on the
// binding exactly where buildRankingInput did: an asset-less binding means
// "candidate-only path".
func toWireCandidate(candidate mediamemory.PoolRankCandidate) rustexec.MediaRankerCandidate {
	fc := candidate.Filtered
	c := fc.Candidate
	wire := rustexec.MediaRankerCandidate{
		CandidateID:           candidate.Key,
		AssetID:               c.AssetID,
		VideoID:               c.VideoID,
		Provider:              c.Provider,
		SourceURL:             c.SourceURL,
		MediaType:             c.MediaType,
		DurationMS:            c.DurationMs,
		CandidateScore:        c.CandidateScore,
		RightsStatus:          string(c.RightsStatus),
		DiscoveryStatus:       string(c.DiscoveryStatus),
		MaterializationStatus: string(c.MaterializationStatus),
		ChannelID:             c.ChannelID,
		Bonus:                 candidate.Bonus,
	}
	if fc.Binding.AssetID != "" {
		wire.Binding = &rustexec.MediaRankerBinding{
			ID:             fc.Binding.ID,
			AssetID:        fc.Binding.AssetID,
			StartMS:        fc.Binding.StartMs,
			EndMS:          fc.Binding.EndMs,
			ApprovalStatus: string(fc.Binding.ApprovalStatus),
			ManualScore:    fc.Binding.ManualScore,
			SemanticScore:  fc.Binding.SemanticScore,
			QualityScore:   fc.Binding.QualityScore,
			SuccessScore:   fc.Binding.SuccessScore,
		}
	}
	return wire
}

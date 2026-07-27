// Package mediamemory — ranker.go is the canonical home for the
// ranker PORT and its default IMPLEMENTATION. The ranker is the
// single canonical seam that turns (binding, candidate, context)
// triples into deterministic orderings; every route that scores
// candidates (resolver, batch, dashboard preview) routes through
// this port.
//
// godlike/06 SSOT (canonical formula + 7-gate vocabulary +
// anti-repetition + diversity picker): the canonical Phase 1.x
// weights, the seven mandatory pre-rank gates, the
// PopulateRepetitionPenalty formula, and the PickTopFromRose
// diversity picker are the load-bearing facts that ANY
// alternative ranking strategy must preserve. They live in
// ranker_weights.go, ranker_gates.go, and ranker_repetition.go
// respectively so each seam is grep-able.
//
// godlike/06 SSOT (extension point): new ranking modes plug in
// via RankingStrategy registered in registry.go. The default
// weights below are the canonical Phase 1.x values; future
// tuning knobs (time-aware, channel-aware, ...) attach via
// RankerConfig and RankingStrategy.Rank(...).
//
// godlike/07 NO-FAKE-AVAILABILITY: Filter() is the canonical
// seam for the seven mandatory gates. A silent no-op pass
// would be a PR-REGRESSION — explicit drop-with-nil envelopes
// the no-op path.
//
// File split (godlike/06 single canonical home per layer):
//   - ranker.go               : Ranker port + defaultRanker + NewDefaultRanker + Score + Filter  ← this file
//   - ranker_types.go         : RankingInput + RankingOutput + RankingVerdict + FilteredCandidate
//   - ranker_weights.go       : DefaultRankWeights + Weights() + VerdictThresholds + DefaultVerdictThresholds + classifyScore + verdictReason
//   - ranker_gates.go         : PreRankGate + 7 gates + RunMandatoryGates + HasAvailableMedia/HasValidDuration/HasSupportedFormat/mediaCandidateIsWellFormed + ComputeDurationFit + PopulateRightsPenalty
//   - ranker_repetition.go    : AntiRepetitionHistoryLimit + 4 channel-recency/ceiling consts + DiversityFinalScoreDelta + RepetitionPenaltyWeights + DefaultRepetitionPenaltyWeights + PopulateRepetitionPenalty + PickTopFromRose + extractCandidateVideoID/ChannelID
package mediamemory

import "context"

// Ranker is the canonical port that turns candidate+context into a
// deterministic ordering. Concrete impl is defaultRanker below.
type Ranker interface {
	// Score computes the final score for a (binding, candidate,
	// context) triple. Implementations MUST honour the canonical
	// formula unless a custom RankingStrategy is registered.
	Score(ctx context.Context, in RankingInput) (RankingOutput, error)

	// Filter applies the mandatory pre-rank filters (license,
	// availability, duration, format, ...) and REMOVES failing
	// candidates. The output slice is strictly a subset of `in`.
	Filter(ctx context.Context, in []FilteredCandidate) ([]FilteredCandidate, error)
}

// ── Default implementation (canonical) ─────────────────────────────

// defaultRanker is the canonical implementation of Ranker. Phase
// 1.x wires the formula + filter; Phase 2 adds anti-repetition and
// diversity-aware re-ordering.
type defaultRanker struct {
	weights    DefaultRankWeights
	thresholds VerdictThresholds
	validators []RankingStrategy
	log        Logger
}

// NewDefaultRanker returns a default ranker using the canonical
// weights. validators register on top, MAY augment (NOT replace)
// the canonical formula until the next version bump.
func NewDefaultRanker(validators []RankingStrategy, log Logger) *defaultRanker {
	if log == nil {
		log = NoopLogger()
	}
	return &defaultRanker{
		weights:    Weights(),
		thresholds: DefaultVerdictThresholds(),
		validators: validators,
		log:        log,
	}
}

// Compile-time assertion: defaultRanker satisfies Ranker.
var _ Ranker = (*defaultRanker)(nil)

// Score computes the canonical formula. Honours the weights from
// Weights(); the resolver pre-populates the per-seat scores (the
// ranker is math-only — no IO).
//
// godlike/07 NO-FAKE-AVAILABILITY: malformed candidates are
// rejected upstream by Filter() (canonical seam per godlike/06
// one-canonical-owner-per-fact). Score() is strictly math-only —
// the caller MUST have gated via Filter first. A regression
// here would result in a math verdict on a malformed envelope;
// defensive callers re-validate via Filter before passing.
//
// The math is the canonical Phase 1.x formula; tampering requires
// a godlike/06 SSOT review.
func (r *defaultRanker) Score(_ context.Context, in RankingInput) (RankingOutput, error) {
	final := (in.SemanticScore * r.weights.Semantic) +
		(in.ExactMatchScore * r.weights.ExactMatch) +
		(in.VisualScore * r.weights.Visual) +
		(in.ManualApprovalScore * r.weights.ManualApproval) +
		(in.QualityScore * r.weights.Quality) +
		(in.HistoricalSuccessScore * r.weights.HistoricalSuccess) +
		(in.DurationFitScore * r.weights.DurationFit) -
		in.RepetitionPenalty -
		in.RightsPenalty

	verdict := classifyScore(final, r.thresholds)
	return RankingOutput{
		FinalScore: final,
		Verdict:    verdict,
		Reason:     verdictReason(verdict),
	}, nil
}

// Filter applies the six mandatory gates (license, availability,
// duration, format, aspect, corrupt-detection, dedup) via the
// canonical RunMandatoryGates helper. godlike/06 SSOT
// (single-canonical-pipeline): the ranker's Filter method MUST
// route through RunMandatoryGates so the gate order +
// gate-name vocabulary stays consistent across callers.
//
// Rows where ANY of the six gates fail are REMOVED. Rows whose
// Candidate fails the well-formed guard (missing AssetID,
// unknown MaterializationStatus/DiscoveryStatus) are ALSO removed
// — the ranker's Score path would otherwise drop them with
// VerdictDrop, doing the work twice and producing confusing
// warning cascades.
//
// The output is strictly a subset of `in`. An empty slice is a
// valid outcome (no candidate survives the gates).
func (r *defaultRanker) Filter(_ context.Context, in []FilteredCandidate) ([]FilteredCandidate, error) {
	out := make([]FilteredCandidate, 0, len(in))
	for _, fc := range in {
		if !mediaCandidateIsWellFormed(fc.Candidate) {
			continue
		}
		if gate := RunMandatoryGates(fc); gate != "" {
			if r.log != nil {
				r.log.Debug("mediamemory: ranker.Filter dropped candidate (gate=%q asset_id=%q)", string(gate), fc.Candidate.AssetID)
			}
			continue
		}
		out = append(out, fc)
	}
	return out, nil
}

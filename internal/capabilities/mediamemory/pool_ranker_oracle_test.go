// ── FROZEN TEST ORACLE — NOT PART OF THE PRODUCTION BINARY ───────────
// oraclePoolRanker is the frozen Go ranker behind the PoolRanker port. It
// exists so resolver tests and the fixture generator can exercise the same
// seam production uses without a Rust worker, and so the Go side of the
// parity comparison stays honest.
//
// Production wires mediamemory.PoolRanker to the mediasampler worker
// (internal/app/modules/media_pool_ranker.go). Nothing in a non-test build can
// reach this implementation: the type lives in a _test.go file.
package mediamemory

import (
	"context"
	"sort"
)

// rankedCandidate is the internal (filter, score) pair used by the oracle's
// sort + pick step.
type rankedCandidate struct {
	key string
	fc  FilteredCandidate
	out RankingOutput
}

// sortByFinalScoreDesc sorts in place by FinalScore DESC, breaking ties by
// AssetID ASC for determinism.
func sortByFinalScoreDesc(in []rankedCandidate) {
	sort.SliceStable(in, func(i, j int) bool {
		return lessRanked(in[i], in[j])
	})
}

// lessRanked orders ranked candidates: higher FinalScore first; ties broken by
// AssetID ASC.
func lessRanked(a, b rankedCandidate) bool {
	if a.out.FinalScore != b.out.FinalScore {
		return a.out.FinalScore > b.out.FinalScore
	}
	return a.fc.Candidate.AssetID < b.fc.Candidate.AssetID
}

// oraclePoolRanker is the frozen Go implementation of PoolRanker.
type oraclePoolRanker struct {
	log Logger
}

// newOraclePoolRanker returns the test-only ranker used by resolver tests.
func newOraclePoolRanker() *oraclePoolRanker { return &oraclePoolRanker{} }

var _ PoolRanker = (*oraclePoolRanker)(nil)

// RankPool mirrors mediasampler::rank for one slot pool: mandatory gates,
// canonical score, repetition penalty, caller bonus applied after the verdict,
// deterministic ordering, diversity-aware winner.
func (o *oraclePoolRanker) RankPool(ctx context.Context, request PoolRankRequest) (PoolRankOutcome, error) {
	ranker := NewDefaultRanker(nil, o.log)
	scene := SceneSpec{DurationMs: request.SceneDurationMS}

	outcome := PoolRankOutcome{Ranked: []PoolRankScore{}, Drops: []PoolRankDrop{}}

	type pending struct {
		key   string
		fc    FilteredCandidate
		bonus float64
	}
	survivors := make([]pending, 0, len(request.Candidates))
	for _, candidate := range request.Candidates {
		fc := candidate.Filtered
		flagged := buildFilterFlags(ctx, []FilteredCandidate{fc}, nil, scene, request.SlotKind)[0]
		if !mediaCandidateIsWellFormed(flagged.Candidate) {
			outcome.Drops = append(outcome.Drops, PoolRankDrop{
				CandidateID: candidate.Key,
				AssetID:     fc.Candidate.AssetID,
				Reason:      "malformed candidate envelope",
			})
			continue
		}
		if gate := RunMandatoryGates(flagged); gate != "" {
			outcome.Drops = append(outcome.Drops, PoolRankDrop{
				CandidateID: candidate.Key,
				AssetID:     fc.Candidate.AssetID,
				Gate:        string(gate),
				Reason:      "failed the " + string(gate) + " gate",
			})
			continue
		}
		survivors = append(survivors, pending{key: candidate.Key, fc: flagged, bonus: candidate.Bonus})
	}

	inputs := make([]RankingInput, 0, len(survivors))
	for _, survivor := range survivors {
		inputs = append(inputs, buildRankingInput(scene, survivor.fc))
	}
	inputs = PopulateRepetitionPenalty(inputs, request.PrevVideoID)

	rose := make([]rankedCandidate, 0, len(inputs))
	for index, in := range inputs {
		score, err := ranker.Score(ctx, in)
		if err != nil {
			return PoolRankOutcome{}, err
		}
		if score.Verdict == VerdictDrop {
			outcome.Drops = append(outcome.Drops, PoolRankDrop{
				CandidateID: survivors[index].key,
				AssetID:     in.Candidate.AssetID,
				Reason:      score.Reason,
			})
			continue
		}
		rose = append(rose, rankedCandidate{
			key: survivors[index].key,
			fc:  FilteredCandidate{Candidate: in.Candidate, Binding: in.Binding},
			// The verdict is classified on the canonical score; the bonus
			// only reorders survivors.
			out: RankingOutput{
				FinalScore: score.FinalScore + survivors[index].bonus,
				Verdict:    score.Verdict,
				Reason:     score.Reason,
			},
		})
	}
	sortByFinalScoreDesc(rose)

	for _, candidate := range rose {
		outcome.Ranked = append(outcome.Ranked, PoolRankScore{
			CandidateID: candidate.key,
			AssetID:     candidate.fc.Candidate.AssetID,
			FinalScore:  candidate.out.FinalScore,
			Verdict:     candidate.out.Verdict,
			Reason:      candidate.out.Reason,
		})
	}
	if len(rose) > 0 {
		top := PickTopFromRose(rose, request.PrevVideoID)
		outcome.Winner = &PoolRankScore{
			CandidateID: top.key,
			AssetID:     top.fc.Candidate.AssetID,
			FinalScore:  top.out.FinalScore,
			Verdict:     top.out.Verdict,
			Reason:      top.out.Reason,
		}
	}
	return outcome, nil
}

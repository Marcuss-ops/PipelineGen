// Package mediamemory — pool_ranker.go is the canonical ranking seam after the
// TICKET-16 cutover. The ranker itself lives in `mediasampler::rank` (Rust): the
// weights, the seven mandatory gates, the verdict bands, the repetition penalty
// and the diversity-aware pick all execute there, and this package only
// projects a slot pool across the boundary.
//
// The contract is deliberately narrow and asymmetric:
//   - the request carries the candidates AND the per-slot facts the ranker
//     needs (scene duration, previous winner, slot kind);
//   - the answer carries a recorded ranking — scores, verdicts, the gate that
//     removed each loser, and the winner.
//
// There is NO Go implementation of this port in the production binary. A nil
// PoolRanker is a configuration error, never a reason to fall back to local
// scoring: the resolver surfaces it as a typed warning instead of inventing a
// ranking. Test binaries keep a frozen Go oracle (`ranker_oracle_test.go` and
// friends) solely to generate and check the parity fixtures in
// contract/media-ranker/v1 — see that file's header.
//
// godlike/06 SSOT (single canonical owner): the ordering rule has exactly one
// owner across the whole repository. Anything that needs an ordering asks this
// port; nothing sorts candidates itself.
package mediamemory

import "context"

// PoolRankCandidate is one candidate entering a slot pool. Bonus is the
// caller's ordering preference (the Brain uses it for slot fitness); it is
// added to the final score after the verdict is classified, so it can reorder
// survivors but can never rescue a dropped candidate. The resolver always
// passes 0, keeping the cascade on the canonical score alone.
type PoolRankCandidate struct {
	// Key is the caller's identity for this pool entry. It MUST be unique
	// within the pool: binding-derived candidates carry no MediaCandidate.ID
	// at all, so the id is not a usable identity across the boundary. The
	// ranker echoes the key back and never interprets it.
	Key      string
	Filtered FilteredCandidate
	Bonus    float64
}

// PoolRankRequest is one slot pool: scene context plus candidates.
type PoolRankRequest struct {
	SceneDurationMS int64
	PrevVideoID     string
	SlotKind        SlotKind
	Candidates      []PoolRankCandidate
}

// PoolRankScore is one surviving candidate in rank order. CandidateID is the
// pool key echoed from the request, not necessarily MediaCandidate.ID.
type PoolRankScore struct {
	CandidateID string
	AssetID     string
	FinalScore  float64
	Verdict     RankingVerdict
	Reason      string
}

// PoolRankDrop is one removed candidate with the gate that removed it (empty
// when the envelope was malformed or the score landed in the drop band).
type PoolRankDrop struct {
	CandidateID string
	AssetID     string
	Gate        string
	Reason      string
}

// PoolRankOutcome is the full per-slot answer. Winner is nil when nothing
// survived; otherwise it points at one of the Ranked entries.
type PoolRankOutcome struct {
	Ranked []PoolRankScore
	Drops  []PoolRankDrop
	Winner *PoolRankScore
}

// PoolRanker ranks one slot pool. Implementations MUST decide gates, scores,
// verdicts, ordering and the winner; callers MUST NOT re-sort or re-gate the
// answer.
type PoolRanker interface {
	RankPool(ctx context.Context, request PoolRankRequest) (PoolRankOutcome, error)
}

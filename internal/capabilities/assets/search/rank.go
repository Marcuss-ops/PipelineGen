// Package search — rank.go implements Wave 21 PR 9 deterministic
// scoring-aware ranking used by Aggregator.Search after dedup.
//
// PR 9 spec: "Ranking globale" with stable ordering across calls
// so cursor pagination is byte-stable (callers can compute offset
// deltas without re-scoring). Score is normalised [0,1]; higher
// wins. Tiebreak is fixed: Source ASC → AssetID ASC.
//
// Stable secondary is required for cursor stability — without it,
// two candidates with the same Score could swap position between
// pages and produce duplicates / gaps in the user's stream.
//
// The sort is implemented as a copy + SliceStable so the input
// slice is not mutated (good citizen at the merge step where the
// dedupIndex's merged slice is shared with future insert paths).
package assets

import "sort"

// RankByScore returns a copy of in sorted by Score DESC, then
// Source ASC, then AssetID ASC. Ties resolve deterministically
// (stable secondary). The input slice is not mutated.
//
// Pre-condition: callers run this after dedup so each Candidate's
// 4-key identity is unique within the slice.
func RankByScore(in []Candidate) []Candidate {
	if len(in) == 0 {
		return []Candidate{}
	}
	out := make([]Candidate, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].AssetID < out[j].AssetID
	})
	return out
}

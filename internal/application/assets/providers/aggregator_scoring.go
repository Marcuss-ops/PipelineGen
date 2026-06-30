package providers

import "sort"

// blendHits sorts the hits slice in place by FinalScore descending.
// In-place sort avoids a second allocation when slice capacity is
// already dominant (the aggregator's hits buffer is sized to
// `len(outcomes)*8` upstream, which is typically >= len(hits)).
//
// Note: when multiple hits share the same FinalScore the sort is
// not stable across distinct provider identities — go's sort.Slice
// is not stable. The tie-breaker by ProviderName is a deterministic
// guard for dashboards that want "what shows up first when ties
// are equal"; not a contract for distributed search rank.
func blendHits(hits []ScoredHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].FinalScore == hits[j].FinalScore {
			return hits[i].ProviderName < hits[j].ProviderName
		}
		return hits[i].FinalScore > hits[j].FinalScore
	})
}

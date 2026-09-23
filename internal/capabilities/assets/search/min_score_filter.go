package search

// FilterByMinScore keeps candidates whose normalized score meets the inclusive
// minimum. A non-positive threshold disables filtering. The helper allocates a
// new slice when filtering so callers do not mutate backend-owned results.
func FilterByMinScore(items []Candidate, minScore float64) []Candidate {
	if minScore <= 0 {
		return items
	}
	out := make([]Candidate, 0, len(items))
	for _, item := range items {
		if item.Score >= minScore {
			out = append(out, item)
		}
	}
	return out
}

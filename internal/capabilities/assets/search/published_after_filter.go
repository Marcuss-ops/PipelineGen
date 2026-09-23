package search

import "time"

// FilterByPublishedAfter keeps candidates with a known publication timestamp
// at or after the inclusive lower bound. Unknown dates fail closed when the
// caller explicitly requests a date constraint.
func FilterByPublishedAfter(items []Candidate, publishedAfter *time.Time) []Candidate {
	if publishedAfter == nil {
		return items
	}
	out := make([]Candidate, 0, len(items))
	for _, item := range items {
		if item.PublishedAt != nil && !item.PublishedAt.Before(*publishedAfter) {
			out = append(out, item)
		}
	}
	return out
}

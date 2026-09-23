// Package search — rank.go implements deterministic ranking for the
// Aggregator. Provider-native discovery sorts are applied only when requested;
// otherwise relevance ordering remains Score DESC, Source ASC, AssetID ASC.
package search

import (
	"sort"
	"strings"
	"time"
)

// RankByScore returns a copy sorted by Score DESC, then Source ASC, then
// AssetID ASC. The input slice is not mutated.
func RankByScore(in []Candidate) []Candidate {
	if len(in) == 0 {
		return []Candidate{}
	}
	out := append([]Candidate(nil), in...)
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

// RankForQuery applies provider sort semantics after deduplication and
// server-side filters. Missing provider metadata sorts after known metadata;
// ties use relevance ranking for deterministic pagination.
func RankForQuery(in []Candidate, q Query) []Candidate {
	mode := strings.ToLower(strings.TrimSpace(q.Filters.Sort))
	if mode == "" || mode == "relevance" {
		return RankByScore(in)
	}
	out := append([]Candidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case "newest", "oldest":
			if a.PublishedAt == nil && b.PublishedAt != nil {
				return false
			}
			if a.PublishedAt != nil && b.PublishedAt == nil {
				return true
			}
			if cmp := comparePublishedAt(a.PublishedAt, b.PublishedAt); cmp != 0 {
				if mode == "newest" {
					return cmp > 0
				}
				return cmp < 0
			}
		case "longest", "shortest":
			if a.DurationMs == 0 && b.DurationMs != 0 {
				return false
			}
			if a.DurationMs != 0 && b.DurationMs == 0 {
				return true
			}
			if a.DurationMs != b.DurationMs {
				if mode == "longest" {
					return a.DurationMs > b.DurationMs
				}
				return a.DurationMs < b.DurationMs
			}
		case "views":
			if a.ViewCount != b.ViewCount {
				return a.ViewCount > b.ViewCount
			}
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.AssetID < b.AssetID
	})
	return out
}

// comparePublishedAt compares two nullable publication timestamps. Known
// timestamps sort ahead of unknown ones in either direction.
func comparePublishedAt(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 0
	case b == nil:
		return 0
	case a.After(*b):
		return 1
	case a.Before(*b):
		return -1
	default:
		return 0
	}
}

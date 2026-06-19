// Package sliceutil provides generic slice manipulation helpers.
package sliceutil

import "strings"

// UniqueStrings returns a copy of the slice with unique strings (case-sensitive).
func UniqueStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// UniqueStringsCI returns a copy of the slice with unique strings (case-insensitive).
func UniqueStringsCI(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		key := strings.ToLower(s)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// MinInt returns the smaller of a and b.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Clamp restricts v to the inclusive [lo, hi] range.
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ClampFloat64 restricts v to the inclusive [lo, hi] range for float64 values.
func ClampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// TrimStrings returns a copy of the slice with all strings trimmed.
func TrimStrings(items []string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = strings.TrimSpace(it)
	}
	return out
}

// AppendUniqueStrings appends values to dst while ensuring uniqueness
// (case-insensitive, trimmed). Empty strings are skipped.
func AppendUniqueStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		seen[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

// UniqueLimitedStrings returns a deduplicated slice (case-insensitive,
// trimmed) capped at limit. limit <= 0 means no limit.
func UniqueLimitedStrings(values []string, limit int) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// GroupSentences groups sentences into chunks of at most size sentences.
// Empty strings are skipped. If size <= 0, defaults to 5.
func GroupSentences(sentences []string, size int) []string {
	if size <= 0 {
		size = 5
	}
	var grouped []string
	var current []string
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		current = append(current, s)
		if len(current) == size {
			grouped = append(grouped, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		grouped = append(grouped, strings.Join(current, " "))
	}
	return grouped
}

// NormalizeFunc canonicalizes a single string. Return "" to drop the item.
type NormalizeFunc func(string) string

// SkipFunc reports whether a normalized string should be filtered out.
type SkipFunc func(string) bool

// noSkip is the zero-value skip filter that keeps everything.
func noSkip(string) bool { return false }

// noNormalize is the zero-value normalize function that returns the input as-is.
func noNormalize(s string) string { return s }

// NormalizeAndDedupe returns a new slice with each item passed through
// `normalize`, filtered by `skip` (if any non-nil predicate returns true),
// and deduplicated in first-seen order. Order of first occurrence is
// preserved in the output.
//
// A nil/empty input returns nil. The returned slice is independent of the
// input. nil function arguments are treated as identity. Returns nil when
// no item survives normalization+skip.
func NormalizeAndDedupe(items []string, normalize NormalizeFunc, skip SkipFunc) []string {
	if len(items) == 0 {
		return nil
	}
	if normalize == nil {
		normalize = noNormalize
	}
	if skip == nil {
		skip = noSkip
	}
	var out []string
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		norm := normalize(raw)
		if norm == "" || skip(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out
}

// MergeNormalizedLists concatenates the given lists, normalizing and
// deduplicating in first-seen order across the whole input. Equivalent to
// NormalizeAndDedupe on a flat concatenation but avoids allocating the
// intermediate flat slice. nil/empty lists contribute nothing. Returns nil
// when no item survives normalization+skip (callers can use the result as
// a drop-in for an empty/nil input).
func MergeNormalizedLists(lists [][]string, normalize NormalizeFunc, skip SkipFunc) []string {
	if len(lists) == 0 {
		return nil
	}
	if normalize == nil {
		normalize = noNormalize
	}
	if skip == nil {
		skip = noSkip
	}
	var out []string
	seen := make(map[string]struct{})
	for _, list := range lists {
		for _, raw := range list {
			norm := normalize(raw)
			if norm == "" || skip(norm) {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}

// MergeNormalizedListsVariadic is a variadic convenience wrapper around
// MergeNormalizedLists for call sites that have a small fixed number of
// lists to merge.
func MergeNormalizedListsVariadic(normalize NormalizeFunc, skip SkipFunc, lists ...[]string) []string {
	return MergeNormalizedLists(lists, normalize, skip)
}


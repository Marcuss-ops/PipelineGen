package platform

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

// TrimStrings returns a copy of the slice with all strings trimmed.
func TrimStrings(items []string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = strings.TrimSpace(it)
	}
	return out
}

// FirstNonEmptySlice returns the first non-empty string from a slice.
func FirstNonEmptySlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
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

// MinInt returns the smaller of a and b.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Clamp restricts v to the inclusive [lo, hi] range.
// If lo > hi, the behavior is undefined (returns v unchanged).
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
// If lo > hi, the behavior is undefined (returns v unchanged).
func ClampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

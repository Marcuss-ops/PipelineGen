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

// TrimStrings returns a copy of the slice with all strings trimmed.
func TrimStrings(items []string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = strings.TrimSpace(it)
	}
	return out
}

// FirstNonEmpty returns the first non-empty string from a slice.
func FirstNonEmpty(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

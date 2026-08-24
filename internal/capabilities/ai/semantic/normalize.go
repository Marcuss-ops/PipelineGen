// Package semantic — NormalizeSearchText compatibility helper.
// The real implementation was removed from remote (commit d61068b3); this minimal version satisfies the canonical contract
// documented in `internal/infrastructure/ai/semantic/normalize_test.go`:
//
//	NormalizeSearchText(parts ...string) string
//
// Contract: variadic parts, lowercased, whitespace-trimmed per token,
// deduplicated, sorted alphabetically, joined with single spaces.
package semantic

import (
	"sort"
	"strings"
)

// NormalizeSearchText joins the given parts into a canonical search text.
// Steps: lowercases each part, splits on whitespace, deduplicates tokens,
// sorts alphabetically, then joins with single spaces.
func NormalizeSearchText(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(parts)*4)
	tokens := make([]string, 0, len(parts)*4)
	for _, p := range parts {
		for _, tok := range strings.Fields(strings.ToLower(strings.TrimSpace(p))) {
			if _, ok := seen[tok]; !ok {
				seen[tok] = struct{}{}
				tokens = append(tokens, tok)
			}
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

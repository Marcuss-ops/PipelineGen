// Package intent is the canonical home of visual-intent resolution
// for the Brain capability.
//
// godlike/06 SSOT: the VisualIntentResolver is the single owner
// of the (text -> VisualIntent) transformation. It performs no IO
// and depends only on the brain types and stdlib.
package intent

import (
	"context"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
)

// VisualIntentResolver is the canonical port that turns a phrase into
// a structured visual intent. It receives both the original text
// (used for capitalisation-dependent heuristics like named-entity
// detection) and the normalized text (used for keywords/concepts).
type VisualIntentResolver interface {
	Resolve(ctx context.Context, language, originalText, normalizedText string) (brain.VisualIntent, error)
	Version() string
}

// cleanEntityToken lowercases a token, trims whitespace and strips
// trailing punctuation so that entities such as "Venere." become
// the canonical "venere".
func cleanEntityToken(tok string) string {
	clean := strings.ToLower(strings.TrimSpace(tok))
	clean = strings.TrimRightFunc(clean, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return clean
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
	return fields
}

func isLikelyEntity(tok string) bool {
	if len(tok) <= 1 {
		return false
	}
	// A token that starts with an uppercase letter is treated as a
	// named-entity candidate. We keep the check on the original
	// token before any case folding is applied.
	r := rune(tok[0])
	return unicode.IsUpper(r)
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

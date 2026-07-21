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

// defaultResolver is the canonical pure implementation. It extracts
// keywords and simple entities without contacting any backend.
type defaultResolver struct{}

// NewDefaultResolver returns the canonical visual-intent resolver.
func NewDefaultResolver() VisualIntentResolver {
	return &defaultResolver{}
}

// Compile-time assertion: defaultResolver satisfies VisualIntentResolver.
var _ VisualIntentResolver = (*defaultResolver)(nil)

// Version returns the canonical intent-resolver version.
func (r *defaultResolver) Version() string {
	return "visual-intent-v1"
}

// Resolve extracts keywords, entities and concepts from the input
// text. The current implementation is intentionally lightweight:
// it treats significant words as keywords and groups contiguous
// capitalised tokens as candidate entities. Future NLP-based
// resolvers implement the same port and are wired at composition
// root without touching callers.
func (r *defaultResolver) Resolve(_ context.Context, language, originalText, normalizedText string) (brain.VisualIntent, error) {
	out := brain.VisualIntent{}

	// Preserve original language hint.
	_ = language

	// Entities are detected from the original text because
	// normalisation folds case and the entity heuristic relies on
	// capitalisation.
	origTokens := tokenize(originalText)
	var entities []string
	for _, tok := range origTokens {
		if !isLikelyEntity(tok) {
			continue
		}
		clean := cleanEntityToken(tok)
		if clean == "" {
			continue
		}
		entities = append(entities, clean)
	}

	// Keywords and concepts are built from the normalised text so
	// they are stable, lowercase and punctuation-stripped.
	normTokens := tokenize(normalizedText)
	var keywords []string
	var concept strings.Builder
	for _, tok := range normTokens {
		clean := strings.ToLower(strings.TrimSpace(tok))
		if clean == "" {
			continue
		}
		keywords = append(keywords, clean)
		if concept.Len() > 0 {
			concept.WriteByte(' ')
		}
		concept.WriteString(clean)
	}

	out.Keywords = uniqueStrings(keywords)
	out.Entities = uniqueStrings(entities)
	out.Concepts = []string{concept.String()}
	return out, nil
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

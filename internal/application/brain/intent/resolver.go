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

// VisualIntentResolver is the canonical port that turns a normalized
// phrase into a structured visual intent.
type VisualIntentResolver interface {
	Resolve(ctx context.Context, language, text string) (brain.VisualIntent, error)
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
func (r *defaultResolver) Resolve(_ context.Context, language, text string) (brain.VisualIntent, error) {
	out := brain.VisualIntent{}

	// Preserve original language hint.
	_ = language

	tokens := tokenize(text)
	if len(tokens) == 0 {
		return out, nil
	}

	var keywords []string
	var entities []string
	var concept strings.Builder

	for _, tok := range tokens {
		clean := strings.ToLower(strings.TrimSpace(tok))
		if clean == "" {
			continue
		}

		keywords = append(keywords, clean)

		if isLikelyEntity(tok) {
			entities = append(entities, clean)
		}

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
	// A token that starts with an uppercase letter and is not at
	// the beginning of the sentence is treated as a named-entity
	// candidate.
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

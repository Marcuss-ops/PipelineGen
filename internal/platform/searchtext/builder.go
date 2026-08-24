// Package searchtext provides the canonical SearchTextBuilder registry.
// Strategies are registered per source type and dispatched by the Registry.
//
// Pattern 0: the compile-time assertion locks the port-implementation
// contract (internal/application/indexing/searchtext.SearchTextBuilder).
package searchtext

import (
	"context"
	"fmt"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
)

// Compile-time assertion: Registry satisfies the application-layer port.
var _ appsearchtext.SearchTextBuilder = (*Registry)(nil)

// Strategy is a per-source search-text builder. It receives the full
// SearchTextInput and returns the assembled text. The zero-value string
// is valid (empty search text → BM25 channel is dropped by the mapper).
type Strategy func(input appsearchtext.SearchTextInput) string

// Registry dispatches Build calls to the strategy registered for the
// asset's Source. Unrecognised sources fall back to the default strategy
// (title + tags join).
type Registry struct {
	strategies map[string]Strategy
}

// NewRegistry creates a Registry with the six canonical strategies
// pre-registered. Callers that need to add custom sources can import
// the per-source constructors and call Register directly.
func NewRegistry() *Registry {
	r := &Registry{
		strategies: map[string]Strategy{
			"youtube":         youtubeStrategy,
			"artlist":         artlistStrategy,
			"voiceover":       voiceoverStrategy,
			"image":           imageStrategy,
			"generated_image": generatedImageStrategy,
			"stock":           stockChunkStrategy,
		},
	}
	return r
}

// Register adds or replaces a strategy for the given source. The source
// key is compared case-sensitively. Pass nil to remove a source (it will
// then fall back to the default strategy at dispatch time).
func (r *Registry) Register(source string, s Strategy) {
	if s == nil {
		delete(r.strategies, source)
		return
	}
	r.strategies[source] = s
}

// Build dispatches to the registered strategy. Unrecognised sources
// use the defaultFallback strategy (title + tags join). The only hard
// error is a nil or empty-AssetID input.
func (r *Registry) Build(ctx context.Context, input appsearchtext.SearchTextInput) (string, error) {
	if input.AssetID == "" {
		return "", fmt.Errorf("searchtext.Registry.Build: AssetID must not be empty")
	}
	s, ok := r.strategies[input.Source]
	if !ok {
		s = defaultFallback
	}
	return s(input), nil
}

// defaultFallback joins title + tags, intended as the safe floor for
// unrecognised or future source types.
func defaultFallback(input appsearchtext.SearchTextInput) string {
	return joinNonEmpty(" ", input.Title, joinTags(input.Tags))
}

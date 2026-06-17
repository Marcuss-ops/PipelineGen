package artlist

import "context"

// SourceProvider defines the interface for Artlist search providers.
// Implementations include DB search, live scraper, Pixabay, and Pexels.
type SourceProvider interface {
	// Search performs a search and returns candidate clips.
	Search(ctx context.Context, term string, limit int) ([]ScraperClip, error)
	// Name returns a human-readable provider name for logging/metrics.
	Name() string
}

// FallbackChain chains multiple SourceProvider instances and tries them
// in order until one returns results. This makes the fallback strategy
// configurable and testable.
type FallbackChain struct {
	providers []SourceProvider
}

// NewFallbackChain creates a fallback chain from the given providers.
func NewFallbackChain(providers ...SourceProvider) *FallbackChain {
	return &FallbackChain{providers: providers}
}

// Search tries each provider in order. Returns the first non-empty result set.
// If all providers fail, returns the last provider's error.
func (fc *FallbackChain) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	var lastErr error
	for _, p := range fc.providers {
		clips, err := p.Search(ctx, term, limit)
		if err == nil && len(clips) > 0 {
			return clips, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

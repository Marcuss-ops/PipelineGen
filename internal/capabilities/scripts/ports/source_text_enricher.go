package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SourceTextEnricher is the canonical port for the source-text cache
// enrichment step. It is executed before BuildPlan so that
// item.Source.SourceText can be short-circuited from cache or left for
// the source resolver to populate.
//
// The enricher is not a new SourceType: it is a shared preparation step
// that all source types may pass through.
type SourceTextEnricher interface {
	// Enrich looks up the source text cache and, on hit, writes the
	// cached text into item.Source.SourceText. The returned result
	// indicates whether the caller may skip source resolution.
	Enrich(ctx context.Context, item *scriptpkg.GenerationItemV2) (EnrichResult, error)

	// Save stores the resolved source text in the cache when the policy
	// allows writes. It is called after source resolution succeeds.
	Save(ctx context.Context, item scriptpkg.GenerationItemV2, text string) error
}

// EnrichResult describes the outcome of a cache lookup.
type EnrichResult int

const (
	// EnrichMiss means the cache did not have a usable entry; the
	// caller should fall through to source resolution.
	EnrichMiss EnrichResult = iota

	// EnrichHit means the cache entry was found and written into
	// item.Source.SourceText. The caller may skip source resolution.
	EnrichHit

	// EnrichBypass means enrichment was disabled or could not run; the
	// caller should continue with source resolution unchanged.
	EnrichBypass
)

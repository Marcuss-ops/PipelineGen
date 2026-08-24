// Package scripts — source_resolver_catalog.go resolves SourceCatalog
// sources into a ResolvedSource. It searches the local media catalog
// for matching clips, then uses ClipSourceBuilder to build context.
//
// FASE-7 move-only refactor (July 2026): the
// deduplicate-and-collect-clip-IDs loop is delegated to the canonical
// ClipSampler port (usecase/clip_sampler_impl.go). The resolver
// normalizes raw LocalCatalog hits into []ports.ClipSamplerCandidate
// and calls the registry's sampler in ONE place. There is no
// resolver-local copy of the dedup+select loop anymore (godlike/06
// SSOT; the user's "vietati tre sampler separati" constraint is
// enforced structurally).
package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// CatalogSourceResolver resolves SourceCatalog sources by searching
// the local media catalog, fetching matching clips, and building
// ClipEvidence via ClipSourceBuilder.
//
// godlike/06 SSOT: the selection/limit/coverage logic is delegated
// to the canonical ClipSampler port (single impl). This resolver
// owns only the per-source raw-to-candidate mapping + the
// post-clipBuilder hydration phase.
type CatalogSourceResolver struct {
	catalogSearch appsearch.LocalCatalogPort
	clipBuilder   *ClipSourceBuilder
	samplerReg    *ClipSamplerRegistry // FASE-7: single source of selection logic
	log           *zap.Logger
}

// NewCatalogSourceResolver creates a CatalogSourceResolver.
// catalogSearch, clipBuilder, and samplerReg must all be non-nil
// (composition root wiring enforces this via
// wire_script_resolvers.go).
func NewCatalogSourceResolver(
	catalogSearch appsearch.LocalCatalogPort,
	clipBuilder *ClipSourceBuilder,
	samplerReg *ClipSamplerRegistry,
	log *zap.Logger,
) *CatalogSourceResolver {
	return &CatalogSourceResolver{
		catalogSearch: catalogSearch,
		clipBuilder:   clipBuilder,
		samplerReg:    samplerReg,
		log:           log,
	}
}

// Resolve searches the catalog and builds a ResolvedSource.
//
// PR 4 (June 2026): resolutionContext is threaded into
// ClipGenerationOptions.Language/Tone/Model/Style/TargetWords.
// Catalog hits don't carry language context so resolutionContext
// is the canonical source of truth.
//
// FASE-7 move-only: dedupe+limit+coverage replaced by a single
// ClipSampler.Select call. Caller-tagged "catalog" propagated
// to the sampler for audit logging only.
func (r *CatalogSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.catalogSearch == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "catalog source resolver: catalog search service not configured",
		}
	}

	query := strings.TrimSpace(src.Query)
	if query == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "catalog source requires a query",
		}
	}

	limit := src.MaxClips
	if limit <= 0 {
		limit = 10
	}
	minCoverage := src.MinCoverage

	start := time.Now()

	// Phase 1: search catalog.
	results, err := r.catalogSearch.SearchAll(ctx, query)
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCatalog,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("catalog search failed: %w", err),
		}
	}

	// Phase 2: build clip context via shared hydration helper.
	// This check is placed BEFORE the sampler selection so that a
	// missing ClipSourceBuilder surfaces as a NoSourceError (missing
	// infrastructure) rather than a SourceResolutionError (no clips
	// found), preserving the contract tested by
	// TestCatalogResolverDeduplicates.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "catalog source resolver: ClipSourceBuilder not configured",
		}
	}

	// FASE-7 move-only: normalize raw catalog results into
	// canonical sampler candidates, then delegate to the single
	// ClipSampler impl. There is no resolver-local copy of the
	// dedup+select+coverage loop anymore.
	candidates := make([]ports.ClipSamplerCandidate, 0, len(results))
	for _, result := range results {
		candidates = append(candidates, ports.ClipSamplerCandidate{
			ClipID: strings.TrimSpace(result.ID),
			Name:   result.Name,
			Score:  result.Score,
			Source: "catalog",
		})
	}

	selection, err := r.samplerReg.SamplerFor(ClipSamplerCallerCatalog).Select(
		ports.ClipSamplerRequest{
			Query:         query,
			Limit:         limit,
			MinCoverage:   minCoverage,
			SourceType:    scriptpkg.SourceCatalog,
			CallingSource: ClipSamplerCallerCatalog,
		},
		candidates,
	)
	if err != nil {
		return nil, err
	}
	if len(selection.ClipIDs) == 0 {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCatalog,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no catalog clips found for query %q", query),
		}
	}
	clipIDs := selection.ClipIDs
	searchItems := selection.SearchItems

	opts := buildSearchClipOpts(src)
	// PR 4: thread operator-side traits from resolutionContext.
	opts.Language = resCtx.Language
	opts.Title = resCtx.Title
	opts.Tone = resCtx.Tone
	opts.Model = resCtx.Model
	opts.Style = resCtx.Style
	opts.TargetWords = resCtx.TargetWords
	// P0 #3 (June 2026): DriveLink required only when caller
	// wants document or scene images.
	opts.RequireDriveLink = resCtx.RequireDriveLink

	resolved, err := buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceCatalog,
		query:         query,
		clipIDs:       clipIDs,
		opts:          opts,
		titleFallback: textutil.FirstNonEmpty(resCtx.Title, query),
		startTime:     start,
	}, r.log)
	if err != nil {
		return nil, err
	}

	resolved.SearchResults = searchItems
	return resolved, nil
}

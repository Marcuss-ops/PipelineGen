// Package scripts — source_resolver_search.go resolves SourceSearch
// sources into a ResolvedSource. It performs semantic search via a
// pluggable SemanticSearchPort (backed by Qdrant in production), then
// uses ClipSourceBuilder to build context from the matched clips.
//
// FASE-7 move-only refactor (July 2026): the
// deduplicate-and-collect-clip-IDs loop is delegated to the canonical
// ClipSampler port (usecase/clip_sampler_impl.go). The resolver
// normalizes raw SemanticSearchResult rows into
// []ports.ClipSamplerCandidate and calls the registry's sampler in
// ONE place. There is no resolver-local copy of the dedup+select
// loop anymore (godlike/06 SSOT; the user's "vietati tre sampler
// separati" constraint is enforced structurally).
package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// SemanticSearchPort is the narrow interface the Search resolver needs
// to find clips by text query. Production wiring maps this to Qdrant
// hybrid search (dense + sparse + transcript). Test wiring uses a
// fake for deterministic results.
type SemanticSearchPort interface {
	// SearchByText performs semantic/hybrid search and returns
	// matched clip results ordered by relevance (descending).
	SearchByText(ctx context.Context, query string, limit int, language string) ([]SemanticSearchResult, error)
}

// SemanticSearchResult is a single clip match from semantic search.
type SemanticSearchResult struct {
	ClipID string  `json:"clip_id"`
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	// Evidence fields are carried from the semantic index so the shared
	// sampler can evaluate its canonical gates before clip hydration.
	Transcript          string
	VisualSummary       string
	MediaType           string
	DriveLink           string
	AvailableByIngest   bool
	AnchorCoverageRatio float64
}

// SearchSourceResolver resolves SourceSearch sources by performing
// semantic search and building ClipEvidence via ClipSourceBuilder.
//
// godlike/06 SSOT: the selection/limit/coverage logic is delegated
// to the canonical ClipSampler port (single impl). This resolver
// owns only the per-source raw-to-candidate mapping + the
// post-clipBuilder hydration phase.
//
// Unit tests currently only exercise Phase 1
// (search port error paths); Phase 2 (ClipSourceBuilder context
// assembly) needs a testable fake ClipSourceBuilder. Same gap
// exists in CatalogSourceResolver tests.
type SearchSourceResolver struct {
	search      SemanticSearchPort
	clipBuilder *ClipSourceBuilder
	samplerReg  *ClipSamplerRegistry // FASE-7: single source of selection logic
	log         *zap.Logger
}

// NewSearchSourceResolver creates a SearchSourceResolver.
// search, clipBuilder, and samplerReg must all be non-nil
// (composition root wiring enforces this via
// wire_script_resolvers.go — the buildScriptSourceResolvers
// factory constructs NewClipSamplerRegistry() once and passes
// it to every resolver).
func NewSearchSourceResolver(
	search SemanticSearchPort,
	clipBuilder *ClipSourceBuilder,
	samplerReg *ClipSamplerRegistry,
	log *zap.Logger,
) *SearchSourceResolver {
	return &SearchSourceResolver{
		search:      search,
		clipBuilder: clipBuilder,
		samplerReg:  samplerReg,
		log:         log,
	}
}

// Resolve performs semantic search and builds a ResolvedSource
// with ClipEvidence and SearchResults.
//
// PR 4 (June 2026): resolutionContext is threaded into
// ClipGenerationOptions.Language/Tone/Model/Style/TargetWords.
// Semantic search results don't carry language context; the
// canonical source is resolutionContext.
//
// FASE-7 move-only: dedupe+limit+coverage replaced by a single
// ClipSampler.Select call. Caller-tagged "search" propagated
// to the sampler for audit logging only.
func (r *SearchSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.search == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "search source resolver: semantic search service not configured",
		}
	}

	query := strings.TrimSpace(src.Query)
	if query == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "search source requires a query",
		}
	}

	limit := src.MaxClips
	if limit <= 0 {
		limit = 10
	}
	minCoverage := src.MinCoverage

	start := time.Now()

	// Phase 1: semantic search. ResolutionContext.Language is the
	// canonical operator target; passing it to SemanticSearchPort
	// allows language-restricted retrieval where supported.
	results, err := r.search.SearchByText(ctx, query, limit, resCtx.Language)
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("semantic search failed: %w", err),
		}
	}

	// FASE-7 move-only: normalize raw SemanticSearchResult rows
	// into canonical sampler candidates, then delegate to the
	// single ClipSampler impl. There is no resolver-local copy
	// of the dedup+select+coverage loop anymore.
	candidates := make([]ports.ClipSamplerCandidate, 0, len(results))
	for _, result := range results {
		candidates = append(candidates, ports.ClipSamplerCandidate{
			ClipID:              strings.TrimSpace(result.ClipID),
			Name:                result.Name,
			Score:               result.Score,
			Source:              "semantic",
			Transcript:          result.Transcript,
			VisualSummary:       result.VisualSummary,
			MediaType:           result.MediaType,
			DriveLink:           result.DriveLink,
			AvailableByIngest:   result.AvailableByIngest,
			AnchorCoverageRatio: result.AnchorCoverageRatio,
		})
	}

	minQualityScore := 0.0
	if src.MinQualityScore != nil {
		minQualityScore = *src.MinQualityScore
	}
	selection, err := r.samplerReg.SamplerFor(ClipSamplerCallerSearch).Select(
		ports.ClipSamplerRequest{
			Query:         query,
			Limit:         limit,
			MinCoverage:   minCoverage,
			MinScore:      minQualityScore,
			Slot:          scriptpkg.ClipSearchSlot{Topic: query},
			SourceType:    scriptpkg.SourceSearch,
			CallingSource: ClipSamplerCallerSearch,
		},
		candidates,
	)
	if err != nil {
		return nil, err
	}
	if len(selection.ClipIDs) == 0 {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no semantic search results for query %q", query),
		}
	}
	clipIDs := selection.ClipIDs
	searchItems := selection.SearchItems

	// Phase 2: build clip context via shared hydration helper.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "search source resolver: ClipSourceBuilder not configured",
		}
	}

	// Semantic search returns relevance order, but a chronological source
	// contract must bind scene N to the clip's real timeline position. The
	// sampler remains the sole selector; this stable reorder only changes
	// the presentation order after selection and before evidence hydration.
	if strings.EqualFold(strings.TrimSpace(src.OrderingStrategy), "chronological") {
		type timedClip struct {
			id    string
			start int64
			pos   int
		}
		timed := make([]timedClip, 0, len(clipIDs))
		for i, id := range clipIDs {
			start := int64(^uint64(0) >> 1)
			if clip, reason := r.clipBuilder.resolveOneClip(ctx, id); reason == clipResolveOK && clip != nil {
				if value, _ := clipTimeline(clip); value >= 0 {
					start = value
				}
			}
			timed = append(timed, timedClip{id: id, start: start, pos: i})
		}
		sort.SliceStable(timed, func(i, j int) bool {
			if timed[i].start == timed[j].start {
				return timed[i].pos < timed[j].pos
			}
			return timed[i].start < timed[j].start
		})
		orderedIDs := make([]string, 0, len(timed))
		orderedItems := make([]scriptpkg.SearchResultItem, 0, len(timed))
		itemsByID := make(map[string]scriptpkg.SearchResultItem, len(searchItems))
		for _, item := range searchItems {
			itemsByID[item.ClipID] = item
		}
		for _, item := range timed {
			orderedIDs = append(orderedIDs, item.id)
			if searchItem, ok := itemsByID[item.id]; ok {
				orderedItems = append(orderedItems, searchItem)
			}
		}
		clipIDs = orderedIDs
		searchItems = orderedItems
	}

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
		sourceType:    scriptpkg.SourceSearch,
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

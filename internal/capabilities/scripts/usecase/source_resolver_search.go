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
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	// Over-fetch before hydration.  The requested limit is the number of
	// clips to accept, not the number of Qdrant rows to inspect: stale or
	// Drive-only rows must not consume the whole search window before the
	// renderability gate can find staged local media.
	searchLimit := limit
	if searchLimit < 20 {
		searchLimit = 20
	}
	// qdrant.search is the semantic-index boundary. The canonical Run clock
	// records it as an OperationReport under source.resolve; no ad-hoc timer.
	var results []SemanticSearchResult
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     scriptgen.StageSourceResolve,
		Component: kernobs.ComponentQdrant,
		Operation: kernobs.OperationSearch,
	}, func(opCtx context.Context) error {
		var searchErr error
		results, searchErr = r.search.SearchByText(opCtx, query, searchLimit, resCtx.Language)
		return searchErr
	}); err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("semantic search failed: %w", err),
		}
	}
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "search source resolver: ClipSourceBuilder not configured",
		}
	}

	// Qdrant's locator-safe payload intentionally does not carry the
	// transcript/description fields needed by the sampler gates. Hydrate
	// the search hits from the canonical SQLite/text-track resolver before
	// selection; this keeps the chain honest: Qdrant selects candidates,
	// SQLite supplies ClipEvidence, and only then can a clip be accepted.
	searchIDs := make([]string, 0, len(results))
	for _, result := range results {
		if id := strings.TrimSpace(result.ClipID); id != "" {
			searchIDs = append(searchIDs, id)
		}
	}
	searchClipOpts := buildSearchClipOpts(src)
	searchClipOpts.Language = strings.TrimSpace(resCtx.Language)
	searchClipOpts.MetadataFallbackByClipID = make(map[string]string, len(results))
	hydratedDetails := make(map[string]scriptpkg.ClipDetail, len(searchIDs))
	for _, id := range searchIDs {
		// Qdrant carries the semantic summary used by the sampler. Pass it
		// through the per-clip hydration options so a Drive-only asset does
		// not need a local transcript row to qualify for a Docs-only job.
		hydrateOpts := *searchClipOpts
		for _, result := range results {
			if strings.TrimSpace(result.ClipID) == id {
				hydrateOpts.MetadataFallbackText = strings.TrimSpace(result.VisualSummary)
				if hydrateOpts.MetadataFallbackText == "" {
					hydrateOpts.MetadataFallbackText = strings.TrimSpace(result.Name)
				}
				hydrateOpts.MetadataFallbackByClipID = map[string]string{id: hydrateOpts.MetadataFallbackText}
				break
			}
		}
		// sqlite.hydrate is the canonical evidence-hydration boundary: Qdrant
		// selects candidates, SQLite supplies ClipEvidence. One operation per
		// hydrated clip so the accumulated work never masquerades as wall time.
		var evidence *scriptpkg.ClipEvidence
		if hydrateErr := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     scriptgen.StageSourceResolve,
			Component: kernobs.ComponentSQLite,
			Operation: kernobs.OperationName("hydrate"),
		}, func(opCtx context.Context) error {
			var buildErr error
			evidence, _, _, buildErr = r.clipBuilder.BuildClipContext(opCtx, []string{id}, &hydrateOpts)
			return buildErr
		}); hydrateErr != nil || evidence == nil || evidence.ClipDetails == nil {
			// A semantic hit without a ready transcript/evidence row is
			// not an accepted clip. Keep searching the Qdrant result set;
			// do not let one stale asset abort otherwise valid evidence.
			continue
		}
		detail, detailOK := evidence.ClipDetails[id]
		if !detailOK {
			continue
		}
		if resCtx.RequireLocalMedia && (strings.TrimSpace(detail.LocalPath) == "" || !containsClipID(evidence.RenderableClipIDs, id)) {
			// A render job requires staged bytes and a renderable ID.
			continue
		}
		if !resCtx.RequireLocalMedia && strings.TrimSpace(detail.DriveLink) == "" {
			// A Docs-only job deliberately does not require local media,
			// but it must still have the canonical Drive locator.
			continue
		}
		if detailOK {
			hydratedDetails[id] = detail
		}
	}

	// FASE-7 move-only: normalize raw SemanticSearchResult rows
	// into canonical sampler candidates, then delegate to the
	// single ClipSampler impl. There is no resolver-local copy
	// of the dedup+select+coverage loop anymore.
	candidates := make([]ports.ClipSamplerCandidate, 0, len(results))
	for _, result := range results {
		id := strings.TrimSpace(result.ClipID)
		if _, ok := hydratedDetails[id]; !ok {
			continue
		}
		detail := scriptpkg.ClipDetail{}
		if hydratedDetails != nil {
			detail = hydratedDetails[id]
		}
		visualSummary := strings.TrimSpace(result.VisualSummary)
		if visualSummary == "" {
			visualSummary = strings.TrimSpace(detail.Description)
		}
		if visualSummary == "" {
			visualSummary = strings.TrimSpace(detail.Name)
		}
		if visualSummary == "" {
			visualSummary = strings.TrimSpace(result.Name)
		}
		transcript := strings.TrimSpace(result.Transcript)
		if transcript == "" {
			transcript = strings.TrimSpace(detail.Transcript)
		}
		driveLink := strings.TrimSpace(result.DriveLink)
		if driveLink == "" {
			driveLink = strings.TrimSpace(detail.DriveLink)
		}
		candidates = append(candidates, ports.ClipSamplerCandidate{
			ClipID:              id,
			Name:                result.Name,
			Score:               result.Score,
			Source:              "semantic",
			Transcript:          transcript,
			VisualSummary:       visualSummary,
			MediaType:           result.MediaType,
			DriveLink:           driveLink,
			AvailableByIngest:   result.AvailableByIngest || driveLink != "",
			AnchorCoverageRatio: result.AnchorCoverageRatio,
		})
		searchClipOpts.MetadataFallbackByClipID[id] = visualSummary
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
		failedGates := make([]string, 0)
		seenFailedGates := make(map[string]struct{})
		candidateSummaries := make([]string, 0, len(candidates))
		for _, record := range selection.Provenance.Records {
			if record.Passed {
				continue
			}
			candidateSummaries = append(candidateSummaries,
				fmt.Sprintf("%s:%s", record.CandidateID, record.GateName+"="+record.Reason))
			if _, seen := seenFailedGates[record.GateName]; seen {
				continue
			}
			seenFailedGates[record.GateName] = struct{}{}
			failedGates = append(failedGates, record.GateName)
		}
		if len(candidateSummaries) == 0 {
			for _, candidate := range candidates {
				candidateSummaries = append(candidateSummaries, fmt.Sprintf(
					"%s:score=%.17g transcript_words=%d visual_summary_runes=%d media_type=%q drive_link=%t available_by_ingest=%t",
					candidate.ClipID,
					candidate.Score,
					len(strings.Fields(candidate.Transcript)),
					len([]rune(candidate.VisualSummary)),
					candidate.MediaType,
					strings.TrimSpace(candidate.DriveLink) != "",
					candidate.AvailableByIngest,
				))
			}
		}
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner: fmt.Errorf(
				"no accepted semantic search results for query %q (qdrant_results=%d hydrated=%d candidates=%d failed_gates=%s candidate_diagnostics=%s)",
				query, len(results), len(hydratedDetails), len(candidates), strings.Join(failedGates, ","), strings.Join(candidateSummaries, "; "),
			),
		}
	}
	clipIDs := selection.ClipIDs
	searchItems := selection.SearchItems

	// Phase 2: build the final canonical ClipEvidence for the selected IDs.
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

	opts := searchClipOpts
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

func containsClipID(ids []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == want {
			return true
		}
	}
	return false
}

// Package scripts — source_resolver_search.go resolves SourceSearch
// sources into a ResolvedSource. It performs semantic search via a
// pluggable SemanticSearchPort (backed by pgvector in production), then
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

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// SemanticSearchPort is the narrow interface the Search resolver needs
// to find clips by text query. Production wiring maps this to pgvector
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

	searchPlan, planErr := buildSearchResolutionPlan(src)
	if planErr != nil {
		planErr.(*scriptpkg.NoSourceError).ItemID = resCtx.ItemID
		return nil, planErr
	}
	query := searchPlan.Query
	start := time.Now()

	results, err := r.searchSemanticClips(ctx, query, searchPlan.SearchLimit, resCtx.Language)
	if err != nil {
		return nil, err
	}
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "search source resolver: ClipSourceBuilder not configured",
		}
	}

	searchClipOpts := buildSearchClipOpts(src)
	searchClipOpts.Language = strings.TrimSpace(resCtx.Language)
	searchClipOpts.MetadataFallbackByClipID = make(map[string]string, len(results))
	hydrated := r.hydrateSearchEvidence(ctx, results, searchClipOpts, resCtx.RequireLocalMedia)
	candidates := buildSamplerCandidates(results, hydrated, searchClipOpts)
	selection, err := r.selectSearchClips(searchPlan, results, hydrated, candidates)
	if err != nil {
		return nil, err
	}
	clipIDs, searchItems := selection.ClipIDs, selection.SearchItems

	// Semantic search returns relevance order, but a chronological source
	// contract binds scene N to the clip's real timeline position. The sampler
	// remains the sole selector; this stable reorder only changes presentation
	// after selection and before evidence hydration.
	if strings.EqualFold(strings.TrimSpace(src.OrderingStrategy), "chronological") {
		clipIDs, searchItems = r.orderSearchChronologically(ctx, clipIDs, searchItems)
	}

	opts := searchClipOpts
	// PR 4: thread operator-side traits from resolutionContext.
	opts.Language = resCtx.Language
	opts.Title = resCtx.Title
	opts.Tone = resCtx.Tone
	opts.Model = resCtx.Model
	opts.Style = resCtx.Style
	opts.TargetWords = resCtx.TargetWords
	// P0 #3 (June 2026): DriveLink required only when caller wants document or
	// scene images.
	opts.RequireDriveLink = resCtx.RequireDriveLink

	resolved, err := buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceSearch,
		query:         query,
		clipIDs:       clipIDs,
		opts:          opts,
		titleFallback: fallbackTitle(resCtx.Title, query),
		startTime:     start,
	}, r.log)
	if err != nil {
		return nil, err
	}

	resolved.SearchResults = searchItems
	return resolved, nil
}

// searchSemanticClips is the vector_search boundary: the canonical Run clock
// records it as an OperationReport under source.resolve, no ad-hoc timer.
func (r *SearchSourceResolver) searchSemanticClips(ctx context.Context, query string, searchLimit int, language string) ([]SemanticSearchResult, error) {
	var results []SemanticSearchResult
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     scriptgen.StageSourceResolve,
		Component: kernobs.ComponentVectorSearch,
		Operation: kernobs.OperationSearch,
	}, func(opCtx context.Context) error {
		var searchErr error
		results, searchErr = r.search.SearchByText(opCtx, query, searchLimit, language)
		return searchErr
	}); err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("semantic search failed: %w", err),
		}
	}
	return results, nil
}

// hydrateSearchEvidence turns pgvector hits into ClipEvidence. pgvector's
// locator-safe payload intentionally omits transcript/description, so SQLite is
// the evidence authority: a hit without a ready row is not an accepted clip,
// and one stale asset must not abort otherwise valid evidence.
func (r *SearchSourceResolver) hydrateSearchEvidence(ctx context.Context, results []SemanticSearchResult, searchClipOpts *ClipGenerationOptions, requireLocalMedia bool) map[string]scriptpkg.ClipDetail {
	searchIDs := make([]string, 0, len(results))
	for _, result := range results {
		if id := strings.TrimSpace(result.ClipID); id != "" {
			searchIDs = append(searchIDs, id)
		}
	}
	hydratedDetails := make(map[string]scriptpkg.ClipDetail, len(searchIDs))
	for _, id := range searchIDs {
		// pgvector carries the semantic summary used by the sampler; pass it
		// through so a Drive-only asset does not need a local transcript row to
		// qualify for a non-render job.
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
			continue
		}
		detail, detailOK := evidence.ClipDetails[id]
		if !detailOK {
			continue
		}
		if requireLocalMedia && (strings.TrimSpace(detail.LocalPath) == "" || !containsClipID(evidence.RenderableClipIDs, id)) {
			continue
		}
		if !requireLocalMedia && strings.TrimSpace(detail.DriveLink) == "" {
			continue
		}
		hydratedDetails[id] = detail
	}
	return hydratedDetails
}

// buildSamplerCandidates normalizes raw hits into the canonical sampler shape.
// The sampler remains the single selector; this only fills per-candidate
// evidence fields with explicit source precedence.
func buildSamplerCandidates(results []SemanticSearchResult, hydrated map[string]scriptpkg.ClipDetail, searchClipOpts *ClipGenerationOptions) []ports.ClipSamplerCandidate {
	candidates := make([]ports.ClipSamplerCandidate, 0, len(results))
	for _, result := range results {
		id := strings.TrimSpace(result.ClipID)
		detail, ok := hydrated[id]
		if !ok {
			continue
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
	return candidates
}

// selectSearchClips delegates selection to the canonical sampler and turns an
// empty result into the auditable diagnostic error (failed gates + candidate
// summaries) that operators use to explain a rejected query.
func (r *SearchSourceResolver) selectSearchClips(plan searchResolutionPlan, results []SemanticSearchResult, hydrated map[string]scriptpkg.ClipDetail, candidates []ports.ClipSamplerCandidate) (ports.ClipSamplerResult, error) {
	selection, err := r.samplerReg.SamplerFor(ClipSamplerCallerSearch).Select(
		ports.ClipSamplerRequest{
			Query:         plan.Query,
			Limit:         plan.Limit,
			MinCoverage:   plan.MinCoverage,
			MinScore:      plan.MinScore,
			Slot:          scriptpkg.ClipSearchSlot{Topic: plan.Query},
			SourceType:    scriptpkg.SourceSearch,
			CallingSource: ClipSamplerCallerSearch,
		},
		candidates,
	)
	if err != nil {
		return ports.ClipSamplerResult{}, err
	}
	if len(selection.ClipIDs) == 0 {
		return ports.ClipSamplerResult{}, noSearchSelectionError(plan.Query, results, hydrated, candidates, selection)
	}
	return selection, nil
}

func noSearchSelectionError(query string, results []SemanticSearchResult, hydrated map[string]scriptpkg.ClipDetail, candidates []ports.ClipSamplerCandidate, selection ports.ClipSamplerResult) error {
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
	return &scriptpkg.SourceResolutionError{
		SourceType:  scriptpkg.SourceSearch,
		Query:       query,
		ResultCount: 0,
		Inner: fmt.Errorf(
			"no accepted semantic search results for query %q (vector_results=%d hydrated=%d candidates=%d failed_gates=%s candidate_diagnostics=%s)",
			query, len(results), len(hydrated), len(candidates), strings.Join(failedGates, ", "), strings.Join(candidateSummaries, "; "),
		),
	}
}

func (r *SearchSourceResolver) orderSearchChronologically(ctx context.Context, clipIDs []string, searchItems []scriptpkg.SearchResultItem) ([]string, []scriptpkg.SearchResultItem) {
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
	return orderedIDs, orderedItems
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

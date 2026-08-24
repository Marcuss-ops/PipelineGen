// Package scripts — source_resolver_curate.go resolves SourceCurate sources.
//
// FASE-7 move-only refactor (July 2026): the
// deduplicate-and-collect-clip-IDs + coverage gate loop is delegated
// to the canonical ClipSampler port (usecase/clip_sampler_impl.go).
// The resolver normalizes raw clip-search hits + hint IDs into
// []ports.ClipSamplerCandidate and calls the registry's sampler in
// ONE place. There is no resolver-local copy of the dedup+select
// loop anymore (godlike/06 SSOT; the user's "vietati tre sampler
// separati" constraint is enforced structurally).
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// ── Semantic-search leg (opt-in, settable) ────────────────────────────────

// AssetSearchPort is the canonical semantic-search leg of
// CurateSourceResolver. Production wires via SetClipSearchPort
// (Qdrant-enabled deployments); the nil-safe setter keeps
// HintClipIDs-only mode working.
//
// The canonical AssetSearchPort carries WorkspaceID + IsSystem;
// callers must propagate the scope from the request envelope.
type AssetSearchPort interface {
	SearchAssets(ctx context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error)
}

// ErrCurateNoClips is the sentinel for "no clips could be resolved".
// The use case wraps this with *scriptpkg.SourceResolutionError at
// the boundary.
var ErrCurateNoClips = fmt.Errorf("curate: no clips found to curate")

// CurateSourceResolver resolves SourceCurate sources.
//
// godlike/06 SSOT: the dedup+limit+min-score logic is delegated
// to the canonical ClipSampler port (single impl). This resolver
// owns only:
//  1. Collection: hits from ClipSearchPort + hint IDs from src.ClipIDs
//  2. Per-source field plumbing (WorkspaceID + IsSystem, MinQualityScore)
//  3. Post-clipBuilder hydration + AllowTextOnly fallback path
type CurateSourceResolver struct {
	clipSearch  AssetSearchPort
	clipBuilder clipContextBuilder
	samplerReg  *ClipSamplerRegistry // FASE-7: single source of selection logic
	log         *zap.Logger
}

// NewCurateSourceResolver creates a CurateSourceResolver.
// clipBuilder and samplerReg must be non-nil (composition root
// enforces this via wire_script_resolvers.go).
func NewCurateSourceResolver(clipBuilder *ClipSourceBuilder, log *zap.Logger, samplerReg *ClipSamplerRegistry) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: clipBuilder,
		samplerReg:  samplerReg,
		log:         log,
	}
}

// SetAssetSearchPort attaches the canonical semantic search adapter.
func (r *CurateSourceResolver) SetAssetSearchPort(port AssetSearchPort) {
	if r == nil {
		return
	}
	r.clipSearch = port
}

// Compile-time assertion: CurateSourceResolver satisfies SourceResolver.
var _ adapters.SourceResolver = (*CurateSourceResolver)(nil)

// Resolve implements SourceResolver.
//
// PR 5 (June 2026): the workspace scope is propagated from
// resCtx (the per-source envelope supplied by the worker) into
// the ClipSearchQuery literal.
//
// FASE-7 move-only: curate delegates dedup+limit to the SINGLE
// sampler impl via the registry. Pre-collection (hits) and
// post-collection (hints) are appended onto the canonical
// []ports.ClipSamplerCandidate slice; the sampler handles
// dedup+limit. hitsSearchItems (curate's SearchResults) is
// built from hits ONLY; the original curate behavior excluded
// hint-only IDs from SearchResults (move-only preserved).
func (r *CurateSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil {
		return nil, fmt.Errorf("CurateSourceResolver: nil receiver")
	}
	if r.clipBuilder == nil {
		return nil, fmt.Errorf("CurateSourceResolver: clipBuilder is nil")
	}

	// Workspace propagation (PR 5).
	const scopeWorkspace = ""
	const scopeIsSystem = true

	query := strings.TrimSpace(src.Query)
	limit := src.MaxClips
	if limit <= 0 {
		limit = 20
	}
	minScore := 0.0
	if src.MinQualityScore != nil {
		minScore = *src.MinQualityScore
	}

	// FASE-7 move-only: collect raw candidates (hits + hints)
	// into the canonical sampler shape. hitsSearchItems is built
	// from hits WITHOUT contributing hint entries — the sampler
	// only sees clip-IDs to dedup, not the metadata audit row.
	candidates := make([]ports.ClipSamplerCandidate, 0)
	hitsSearchItems := make([]scriptpkg.SearchResultItem, 0)
	seenForItems := make(map[string]struct{})

	if src.Search && r.clipSearch != nil {
		hits, searchErr := r.clipSearch.SearchAssets(ctx, ports.AssetSearchQuery{
			Query:       query,
			Source:      src.SourceFilter,
			Category:    "",
			MediaType:   src.MediaTypeFilter,
			WorkspaceID: scopeWorkspace,
			IsSystem:    scopeIsSystem,
			Limit:       limit,
			MinScore:    minScore,
		})
		if searchErr != nil {
			if r.log != nil {
				r.log.Warn("CurateSourceResolver: clip search failed, falling back to hints",
					zap.Error(searchErr))
			}
		} else {
			for _, h := range hits {
				id := strings.TrimSpace(h.AssetID)
				if id == "" {
					continue
				}
				candidates = append(candidates, ports.ClipSamplerCandidate{
					ClipID: id,
					Name:   h.Name,
					Score:  h.Score,
					Source: h.Source,
				})
				if _, dup := seenForItems[id]; !dup {
					seenForItems[id] = struct{}{}
					hitsSearchItems = append(hitsSearchItems, scriptpkg.SearchResultItem{
						ClipID: id,
						Name:   h.Name,
						Score:  h.Score,
						Source: h.Source,
					})
				}
			}
		}
	}

	if src.Search && r.clipSearch == nil && r.log != nil {
		r.log.Info("CurateSourceResolver: Search=true but ClipSearchPort not wired (HintClipIDs-only)")
	}

	// Hints become candidates too (zero-score) so the sampler
	// dedupes hit-vs-hint collisions. They DO NOT contribute to
	// hitsSearchItems (move-only: original curate SearchResults
	// excluded hint IDs).
	for _, id := range src.ClipIDs {
		id := strings.TrimSpace(id)
		if id == "" {
			continue
		}
		candidates = append(candidates, ports.ClipSamplerCandidate{
			ClipID: id,
			Name:   "",
			Score:  0,
			Source: "hint",
		})
	}

	if len(candidates) == 0 {
		if !src.AllowTextOnly {
			return nil, &scriptpkg.SourceResolutionError{
				SourceType:  scriptpkg.SourceCurate,
				Query:       query,
				ResultCount: 0,
				Inner:       ErrCurateNoClips,
			}
		}
		return &scriptpkg.ResolvedSource{
			Type:            scriptpkg.SourceCurate,
			Topic:           query,
			Title:           resCtx.Title,
			Language:        resCtx.Language,
			SourceText:      "",
			SearchResults:   hitsSearchItems,
			GroundingPolicy: src.GroundingPolicy,
		}, nil
	}

	// Delegate dedup+limit to the canonical sampler. MinCoverage
	// is 0 for curate (no coverage gate in the original code);
	// MinScore is enforced upstream by the qdrant adapter, so
	// 0 here matches the original curate behavior (move-only
	// preserved).
	selection, err := r.samplerReg.SamplerFor(ClipSamplerCallerCurate).Select(
		ports.ClipSamplerRequest{
			Query:         query,
			Limit:         limit,
			MinScore:      0,
			SourceType:    scriptpkg.SourceCurate,
			CallingSource: ClipSamplerCallerCurate,
		},
		candidates,
	)
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCurate,
			Query:       query,
			ResultCount: 0,
			Inner:       err,
		}
	}

	clipIDs := selection.ClipIDs
	searchItems := hitsSearchItems

	opts := &ClipGenerationOptions{
		Language:           resCtx.Language,
		Title:              resCtx.Title,
		Tone:               resCtx.Tone,
		Model:              resCtx.Model,
		Style:              resCtx.Style,
		TargetWords:        resCtx.TargetWords,
		NumClips:           resCtx.NumClips,
		SegmentWords:       resCtx.SegmentWords,
		Segments:           append([]scriptpkg.ScriptSegment(nil), resCtx.Segments...),
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
		// P0 #3 (June 2026): DriveLink required only when caller
		// wants document or scene images.
		RequireDriveLink: resCtx.RequireDriveLink,
	}

	clipEvidence, resolvedTitle, sourceText, buildErr := r.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCurate,
			Query:       query,
			ResultCount: len(clipIDs),
			Inner:       buildErr,
		}
	}

	// P1 #9 (June 2026): resolvedTitle is the plan-derived title from
	// BuildClipContext. Fall back to the resolution context title.
	title := strings.TrimSpace(resolvedTitle)
	if title == "" {
		title = resCtx.Title
	}
	if title == "" {
		title = query
	}
	modelSourceText := sourceText
	if clipEvidence != nil {
		modelSourceText = strings.TrimSpace(clipEvidence.ModelSourceText())
	}

	return &scriptpkg.ResolvedSource{
		Type:            scriptpkg.SourceCurate,
		Topic:           query,
		Title:           title,
		Language:        resCtx.Language,
		SourceText:      modelSourceText,
		ClipEvidence:    clipEvidence,
		SearchResults:   searchItems,
		GroundingPolicy: src.GroundingPolicy,
	}, nil
}

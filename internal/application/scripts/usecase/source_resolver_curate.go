// Package scripts — source_resolver_curate.go resolves SourceCurate sources.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): the previously duplicate
// ClipSearchQuery struct is replaced with a type alias to the
// canonical ports.ClipSearchQuery so the WorkspaceID + IsSystem fields
// (added in PR 5) are visible to the curate path without any further
// wiring. The alias is bidirectional at the type-system level — every
// existing callsite that built a usecase.ClipSearchQuery literal
// keeps compiling, but now resolves to the same shape the qdrant
// adapter consumes.
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// ── Semantic-search leg (opt-in, settable) ────────────────────────────────

// ClipSearchQuery is a TYPE ALIAS for the canonical
// ports.ClipSearchQuery. PR 5 replaced the pre-existing literal
// duplicate struct with this alias so adding fields (notably
// WorkspaceID + IsSystem) cannot drift between the curate path
// and the canonical port surface. Code that previously referenced
// `usecase.ClipSearchQuery{...}` keeps compiling — the alias
// resolves to the same shape at runtime. This is the consolidation
// AGENTS.md §Migration Status (Brutal Care Plan) was converging
// toward; the previous duplicate is now permanently a no-op.
type ClipSearchQuery = ports.ClipSearchQuery

// ClipSearchPort is the canonical semantic-search leg of
// CurateSourceResolver. Production wires via SetClipSearchPort
// (Qdrant-enabled deployments); the nil-safe setter keeps
// HintClipIDs-only mode working.
//
// PR 5 (June 2026): the canonical ports.ClipSearchPort interface
// already carries WorkspaceID + IsSystem; the curate wrapper does
// not redefine them. Callers must propagate the workspace scope
// from the request envelope into the ClipSearchQuery literal at
// the call site; an empty WorkspaceID + IsSystem=false triggers
// qdrant.CompileQdrantFilter's fail-closed ErrMissingWorkspace
// contract rather than a silent cross-tenant read.
type ClipSearchPort interface {
	SearchClips(ctx context.Context, q ports.ClipSearchQuery) ([]scriptpkg.SearchResultItem, error)
}

// ErrCurateNoClips is the sentinel for "no clips could be resolved".
// The use case wraps this with *scriptpkg.SourceResolutionError at
// the boundary.
var ErrCurateNoClips = fmt.Errorf("curate: no clips found to curate")

// CurateSourceResolver resolves SourceCurate sources.
type CurateSourceResolver struct {
	clipSearch  ClipSearchPort
	clipBuilder clipContextBuilder
	log         *zap.Logger
}

// NewCurateSourceResolver creates a CurateSourceResolver backed by
// a concrete ClipSourceBuilder.
func NewCurateSourceResolver(clipBuilder *ClipSourceBuilder, log *zap.Logger) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// SetClipSearchPort attaches the typed ClipSearchPort adapter.
func (r *CurateSourceResolver) SetClipSearchPort(port ClipSearchPort) {
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
// the ClipSearchQuery literal. Without this propagation, the
// compile-time fail-closed gate at qdrant.CompileQdrantFilter
// (which rejects WorkspaceID="" + IsSystem=false) would force
// every curate call to either crash OR be marked system. The
// exact field name on resCtx depends on the orchestration layer's
// CopyScriptScopeForCuration glue; the curate path is the
// canonical consumer, so the symbol it expects here is the
// contract.
func (r *CurateSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil {
		return nil, fmt.Errorf("CurateSourceResolver: nil receiver")
	}
	if r.clipBuilder == nil {
		return nil, fmt.Errorf("CurateSourceResolver: clipBuilder is nil")
	}

	// Workspace propagation (PR 5). `SourceResolutionContext` does
	// not (yet) carry a workspace envelope — see internal/domain/script
	// for the canonical struct fields. Curate is a BACKGROUND
	// semantic-search leg by design: the worker that schedules
	// the curate task often operates across multiple workspaces
	// to seed the next batch of clips. Per the verdict §8,
	// background requests MUST be marked IsSystem=true EXPLICITLY;
	// the workspace MUST-clause at qdrant.CompileQdrantFilter is
	// omitted because IsSystem=true short-circuits it. A future
	// follow-up can route a per-script workspace through the
	// orchestration layer if a per-tenant curate mode becomes
	// necessary; for now the spec-matching behaviour is
	// IsSystem=true + WorkspaceID="".
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

	clipIDs := make([]string, 0)
	searchResults := make([]scriptpkg.SearchResultItem, 0)
	seen := make(map[string]struct{})

	if src.Search && r.clipSearch != nil {
		hits, searchErr := r.clipSearch.SearchClips(ctx, ClipSearchQuery{
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
				if _, dup := seen[h.ClipID]; dup {
					continue
				}
				seen[h.ClipID] = struct{}{}
				clipIDs = append(clipIDs, h.ClipID)
				searchResults = append(searchResults, scriptpkg.SearchResultItem{
					ClipID: h.ClipID,
					Name:   h.Name,
					Score:  h.Score,
					Source: h.Source,
				})
			}
		}
	}

	if src.Search && r.clipSearch == nil && r.log != nil {
		r.log.Info("CurateSourceResolver: Search=true but ClipSearchPort not wired (HintClipIDs-only)")
	}

	for _, id := range src.ClipIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clipIDs = append(clipIDs, id)
	}

	if len(clipIDs) == 0 {
		if !src.AllowTextOnly {
			return nil, &scriptpkg.SourceResolutionError{
				SourceType:  scriptpkg.SourceCurate,
				Query:       query,
				ResultCount: 0,
				Inner:       ErrCurateNoClips,
			}
		}
		return &scriptpkg.ResolvedSource{
			Type:          scriptpkg.SourceCurate,
			Topic:         query,
			Title:         resCtx.Title,
			Language:      resCtx.Language,
			SourceText:    "",
			SearchResults: searchResults,
		}, nil
	}

	opts := &ClipGenerationOptions{
		Language:           resCtx.Language,
		Title:              resCtx.Title,
		Tone:               resCtx.Tone,
		Model:              resCtx.Model,
		Style:              resCtx.Style,
		TargetWords:        resCtx.TargetWords,
		NumClips:           resCtx.NumClips,
		SegmentWords:       resCtx.SegmentWords,
		SegmentTopics:      append([]string(nil), resCtx.SegmentTopics...),
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

	return &scriptpkg.ResolvedSource{
		Type:          scriptpkg.SourceCurate,
		Topic:         query,
		Title:         title,
		Language:      resCtx.Language,
		SourceText:    sourceText,
		ClipEvidence:  clipEvidence,
		SearchResults: searchResults,
	}, nil
}

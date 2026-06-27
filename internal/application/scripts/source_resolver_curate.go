// Package scripts — source_resolver_curate.go resolves SourceCurate
// sources. It owns the union of semantic search (via ClipSearchPort,
// opt-in via SourceSpec.Search) + HintClipIDs dedup + ClipSourceBuilder
// context building.
//
// PR E (June 2026): extracted from MediaCurator.Curate. Registered in
// SourceRegistry so GenerateOneUseCase.Execute can transparently
// resolve curate sources through the unified pipeline.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// CurateSourceResolver resolves SourceCurate sources. It owns:
//   - clipSearch (optional, settable) — the semantic search leg
//   - clipBuilder — the clip context building leg
//
// Both legs are nil-safe: a nil clipSearch means only HintClipIDs
// are consumed (legacy behaviour). A nil clipBuilder means the
// resolver can't build context and returns a typed error.
type CurateSourceResolver struct {
	clipSearch ClipSearchPort
	clipBuilder clipContextBuilder
	log         *zap.Logger
}

// NewCurateSourceResolver creates a CurateSourceResolver backed by
// a concrete ClipSourceBuilder. clipSearch is set later via
// SetClipSearchPort (called from wire_script.go when Qdrant is wired).
// clipBuilder must be non-nil; clipSearch is set later via SetClipSearchPort.
func NewCurateSourceResolver(clipBuilder *ClipSourceBuilder, log *zap.Logger) *CurateSourceResolver {
	return &CurateSourceResolver{
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// SetClipSearchPort attaches the typed ClipSearchPort adapter.
// Called from wire_script.go when Qdrant is enabled and the Ollama
// embedder is available. nil is the safe no-op (HintClipIDs-only legacy path).
func (r *CurateSourceResolver) SetClipSearchPort(port ClipSearchPort) {
	if r == nil {
		return
	}
	r.clipSearch = port
}

// Compile-time assertion: CurateSourceResolver satisfies SourceResolver.
var _ SourceResolver = (*CurateSourceResolver)(nil)

// Resolve implements SourceResolver. It unions semantic search results
// with HintClipIDs, deduplicates, then builds clip context via
// ClipSourceBuilder.BuildClipContext. Returns ErrCurateNoClips when
// both legs produce zero clips and AllowTextOnly is false.
func (r *CurateSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil {
		return nil, fmt.Errorf("CurateSourceResolver: nil receiver")
	}
	if r.clipBuilder == nil {
		return nil, fmt.Errorf("CurateSourceResolver: clipBuilder is nil")
	}

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

	// Semantic search leg (opt-in via src.Search + ClipSearchPort wired).
	if src.Search && r.clipSearch != nil {
		hits, searchErr := r.clipSearch.SearchClips(ctx, ClipSearchQuery{
			Query:     query,
			Source:    src.SourceFilter,
			Category:  "",
			MediaType: src.MediaTypeFilter,
			Limit:     limit,
			MinScore:  minScore,
		})
		if searchErr != nil {
			if r.log != nil {
				r.log.Warn("CurateSourceResolver: clip search failed, falling back to hints",
					zap.Error(searchErr))
			}
		} else {
			for _, h := range hits {
				if _, dup := seen[h.AssetID]; dup {
					continue
				}
				seen[h.AssetID] = struct{}{}
				clipIDs = append(clipIDs, h.AssetID)
				searchResults = append(searchResults, scriptpkg.SearchResultItem{
					ClipID: h.AssetID,
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

	// Merge HintClipIDs (dedup against search hits).
	for _, id := range src.ClipIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clipIDs = append(clipIDs, id)
	}

	// ErrCurateNoClips contract: return structured error when no clips
	// resolve and the caller did not opt into the text-only fallback.
	if len(clipIDs) == 0 {
		if !src.AllowTextOnly {
			return nil, &scriptpkg.SourceResolutionError{
				SourceType:  scriptpkg.SourceCurate,
				Query:       query,
				ResultCount: 0,
				Inner:       ErrCurateNoClips,
			}
		}
		// AllowTextOnly=true with zero clips — resolver succeeds with
		// minimal resolved source (no clip evidence, empty source text).
		return &scriptpkg.ResolvedSource{
			Type:          scriptpkg.SourceCurate,
			Topic:         query,
			SourceText:    "",
			SearchResults: searchResults,
		}, nil
	}

	// Build clip context via ClipSourceBuilder.
	opts := &ClipGenerationOptions{
		Language:          src.Guidelines, // passthrough (Guidelines reused as style instructions)
		TranscriptPolicy:  src.TranscriptPolicy,
		OrderingStrategy:  src.OrderingStrategy,
		MinQualityScore:   0,
		MinTranscriptWords: 0,
	}

	pack, narrativePlan, sourceText, buildErr := r.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCurate,
			Query:       query,
			ResultCount: len(clipIDs),
			Inner:       buildErr,
		}
	}

	_ = narrativePlan // carried via ResolvedSource for downstream consumers

	// Build ClipEvidence from the resolved clips + pack info.
	clipEvidence := BuildClipEvidence(pack, sourceText)

	return &scriptpkg.ResolvedSource{
		Type:          scriptpkg.SourceCurate,
		Topic:         query,
		SourceText:    sourceText,
		ClipEvidence:  clipEvidence,
		SearchResults: searchResults,
	}, nil
}

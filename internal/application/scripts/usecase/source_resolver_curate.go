// Package scripts — source_resolver_curate.go resolves SourceCurate sources.
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// ── Semantic-search leg (opt-in, settable) ────────────────────────────────

// ClipSearchQuery is the input shape for ClipSearchPort.SearchClips.
type ClipSearchQuery struct {
	Query     string
	Source    string
	Category  string
	MediaType string
	Limit     int
	MinScore  float64
}

// ClipSearchPort is the canonical semantic-search leg of
// CurateSourceResolver. Production wires via SetClipSearchPort
// (Qdrant-enabled deployments); the nil-safe setter keeps
// HintClipIDs-only mode working.
type ClipSearchPort interface {
	SearchClips(ctx context.Context, q ClipSearchQuery) ([]scriptpkg.SearchResultItem, error)
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
var _ SourceResolver = (*CurateSourceResolver)(nil)

// Resolve implements SourceResolver.
func (r *CurateSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
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

	_ = narrativePlan

	clipEvidence := BuildClipEvidence(pack, sourceText)

	title := resCtx.Title
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

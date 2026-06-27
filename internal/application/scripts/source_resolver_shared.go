// Package scripts — source_resolver_shared.go extracts the shared
// Phase 2 hydration logic used by Clips, Catalog, and Search
// resolvers. Every non-text resolver follows the same pattern:
//
//  1. Resolve clip IDs (Phase 1 — resolver-specific).
//  2. Build opts → BuildClipContext → BuildClipEvidence → derive title
//     → compute fingerprint → log → return ResolvedSource (Phase 2 —
//     shared here).
//
// The helper keeps the three resolvers in sync: any new field added
// to ResolvedSource or ClipGenerationOptions is added once here and
// consumed identically by all three.
package scripts

import (
	"context"
	"fmt"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// clipContextBuilder is the minimal surface the shared Phase 2
// hydration needs from ClipSourceBuilder. Abstracted so the
// shared code doesn't depend on the full concrete type (which
// carries reranker, ollamaClient, etc.) and so test fakes can
// implement just this one method.
type clipContextBuilder interface {
	BuildClipContext(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (pack interface{}, plan *NarrativePlan, sourceText string, err error)
}

// resolvedClipParams carries the resolver-specific inputs into
// the shared Phase 2 hydration.
type resolvedClipParams struct {
	sourceType    scriptpkg.SourceType
	query         string // for log + error messages
	clipIDs       []string
	opts          *ClipGenerationOptions
	titleFallback string    // used when plan has no title
	startTime     time.Time // Phase 1 start — total elapsed is Phase 1 + Phase 2
}

// buildSearchClipOpts constructs the canonical ClipGenerationOptions
// for query-based resolvers (Catalog, Search). Both use the same
// source-level filters (transcript policy, ordering, quality,
// transcript words). Clips sources go through a different path
// (they carry Language/Title context from the caller).
func buildSearchClipOpts(src scriptpkg.SourceSpec) *ClipGenerationOptions {
	return &ClipGenerationOptions{
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		NumClips:           src.NumClips,
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
	}
}

// buildResolvedClipSource is the single canonical Phase 2 hydration
// path for all non-text resolvers. It:
//
//  1. Calls ClipSourceBuilder.BuildClipContext.
//  2. Builds typed ClipEvidence.
//  3. Derives title (plan.Title → fallback).
//  4. Computes fingerprint.
//  5. Logs resolution metrics (total elapsed: Phase 1 + Phase 2).
//  6. Returns a fully-populated ResolvedSource.
//
// Every resolver that produces ClipEvidence must route through this
// helper. Direct ResolvedSource construction in a resolver file is
// a layering violation (PR 5, June 2026).
func buildResolvedClipSource(
	ctx context.Context,
	builder clipContextBuilder,
	src scriptpkg.SourceSpec,
	p resolvedClipParams,
	log *zap.Logger,
) (*scriptpkg.ResolvedSource, error) {
	pack, plan, sourceText, buildErr := builder.BuildClipContext(ctx, p.clipIDs, p.opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  p.sourceType,
			Query:       p.query,
			ResultCount: len(p.clipIDs),
			Inner:       fmt.Errorf("clip context build failed: %w", buildErr),
		}
	}

	evidence := BuildClipEvidence(pack, sourceText)

	// Title: plan title first, then caller-provided fallback.
	title := ""
	if plan != nil {
		title = plan.Title
	}
	if title == "" {
		title = p.titleFallback
	}

	fingerprint := computeSourceFingerprint(src, evidence)

	if log != nil {
		elapsed := time.Since(p.startTime)
		log.Info(string(p.sourceType)+" source resolved",
			zap.String("query", p.query),
			zap.Int("clips_resolved", len(p.clipIDs)),
			zap.Int64("total_elapsed_ms", elapsed.Milliseconds()))
	}

	return &scriptpkg.ResolvedSource{
		Type:         p.sourceType,
		Topic:        title,
		Title:        title,
		SourceText:   sourceText,
		ClipEvidence: evidence,
		Fingerprint:  fingerprint,
	}, nil
}

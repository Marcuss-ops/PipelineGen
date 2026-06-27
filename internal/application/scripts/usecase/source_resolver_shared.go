// Package scripts — source_resolver_shared.go extracts the shared
// Phase 2 hydration logic used by Clips, Catalog, and Search resolvers.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// ── Helpers (PR 5 — shared between all clip-aware resolvers) ───────────────

// BuildClipEvidence converts a clip pack + source text into a typed
// *scriptpkg.ClipEvidence. Returns nil when both inputs are empty
// (caller treats nil evidence as "no clip context"). PR 5: real
// implementation lands when the canonical model-output contract
// consolidates (godlike/06 §metadata).

// computeSourceFingerprint is the deterministic cache-key component
// derived from SourceSpec + ClipEvidence. PR 5 stub: real hashing
// lives in the engine's cache-key derivation; this is the resolver-
// side shortcut that lets the use case log a stable fingerprint
// without invoking the engine.

// ── Shared types (Phase 2 hydration plumbing) ──────────────────────────

type clipContextBuilder interface {
	BuildClipContext(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (pack interface{}, plan *NarrativePlan, sourceText string, err error)
}

type resolvedClipParams struct {
	sourceType    scriptpkg.SourceType
	query         string
	clipIDs       []string
	opts          *ClipGenerationOptions
	titleFallback string
	startTime     time.Time
}

func buildSearchClipOpts(src scriptpkg.SourceSpec) *ClipGenerationOptions {
	return &ClipGenerationOptions{
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
	}
}

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

	title := ""
	if plan != nil {
		title = plan.Title
	}
	if title == "" {
		title = strings.TrimSpace(p.titleFallback)
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

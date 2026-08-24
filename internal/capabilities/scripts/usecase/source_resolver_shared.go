// Package scripts — source_resolver_shared.go extracts the shared
// Phase 2 hydration logic used by Clips, Catalog, and Search resolvers.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/ptrutil"

	"go.uber.org/zap"
)

// ── Shared types (Phase 2 hydration plumbing) ──────────────────────────

type clipContextBuilder interface {
	// P1 #9 (June 2026): second return is the resolved title string
	// (was *NarrativePlan — only plan.Title was ever consumed).
	BuildClipContext(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (*scriptpkg.ClipEvidence, string, string, error)
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
		TranscriptPolicy:      src.TranscriptPolicy,
		OrderingStrategy:      src.OrderingStrategy,
		MinQualityScore:       ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords:    ptrutil.DerefOr(src.MinTranscriptWords, 0),
		AllowMetadataFallback: true,
	}
}

func buildResolvedClipSource(
	ctx context.Context,
	builder clipContextBuilder,
	src scriptpkg.SourceSpec,
	p resolvedClipParams,
	log *zap.Logger,
) (*scriptpkg.ResolvedSource, error) {
	evidence, resolvedTitle, sourceText, buildErr := builder.BuildClipContext(ctx, p.clipIDs, p.opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  p.sourceType,
			Query:       p.query,
			ResultCount: len(p.clipIDs),
			Inner:       fmt.Errorf("clip context build failed: %w", buildErr),
		}
	}
	if p.sourceType == scriptpkg.SourceClips && (evidence == nil || len(evidence.AcceptedClipIDs) == 0) {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  p.sourceType,
			Query:       p.query,
			ResultCount: len(p.clipIDs),
			Inner:       fmt.Errorf("clip context build returned empty clip evidence"),
		}
	}

	// P1 #9 (June 2026): resolvedTitle is the plan-derived title from
	// BuildClipContext. Fall back to the resolver-supplied title.
	title := strings.TrimSpace(resolvedTitle)
	if title == "" {
		title = strings.TrimSpace(p.titleFallback)
	}

	fingerprint := BuildClipFingerprint(src, evidence)
	modelSourceText := strings.TrimSpace(sourceText)
	if evidence != nil {
		if evidenceText := strings.TrimSpace(evidence.ModelSourceText()); evidenceText != "" {
			modelSourceText = evidenceText
		}
	}
	// A clip source may intentionally omit transcripts when the caller
	// supplied explicit source_text. Preserve that authoritative text as
	// the model input instead of silently returning an empty source.
	if modelSourceText == "" {
		modelSourceText = strings.TrimSpace(src.SourceText)
	}

	if log != nil {
		elapsed := time.Since(p.startTime)
		log.Info(string(p.sourceType)+" source resolved",
			zap.String("query", p.query),
			zap.Int("clips_resolved", len(p.clipIDs)),
			zap.Int64("total_elapsed_ms", elapsed.Milliseconds()))
	}

	return &scriptpkg.ResolvedSource{
		Type:            p.sourceType,
		Topic:           title,
		Title:           title,
		SourceText:      modelSourceText,
		Segments:        scriptpkg.CloneScriptSegments(p.opts.Segments),
		ClipEvidence:    evidence,
		Fingerprint:     fingerprint,
		GroundingPolicy: src.GroundingPolicy,
	}, nil
}

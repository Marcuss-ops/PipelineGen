// Package scripts — source_resolver_clips.go resolves SourceClips
// sources into a ResolvedSource. It uses ClipSourceBuilder to fetch
// clips by ID and build context, then converts the result into
// typed ClipEvidence.
package usecase

import (
	"context"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ClipsSourceResolver resolves SourceClips (explicit clip IDs)
// into a ResolvedSource with ClipEvidence.
type ClipsSourceResolver struct {
	clipBuilder *ClipSourceBuilder
	log         *zap.Logger
}

// NewClipsSourceResolver creates a ClipsSourceResolver backed by
// the canonical ClipSourceBuilder. clipBuilder must be non-nil.
func NewClipsSourceResolver(clipBuilder *ClipSourceBuilder, log *zap.Logger) *ClipsSourceResolver {
	return &ClipsSourceResolver{
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// Resolve fetches clips by explicit ID and builds a ResolvedSource
// with ClipEvidence.
//
// PR 4 (June 2026): resolutionContext is now threaded into
// ClipGenerationOptions.Language/Tone/Model/Style/TargetWords. The
// resolver reads these explicitly from the resolution context (the
// canonical source of operator-side traits) instead of hijacking
// SourceSpec.Guidelines.
func (r *ClipsSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "clips source resolver: ClipSourceBuilder not configured",
		}
	}

	if len(src.ClipIDs) == 0 {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "clips source requires at least one clip_id",
		}
	}

	start := time.Now()

	opts := &ClipGenerationOptions{
		// PR 4: language/tone/model/style/target words come from
		// resolutionContext — the canonical operator-side context.
		// src.Guidelines is intentionally NOT consumed here
		// (curate resolver historically did this; the bug class
		// is now centralised: any resolver reading Guidelines as
		// a technical field is wrong).
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

	return buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceClips,
		query:         strings.Join(src.ClipIDs, ","),
		clipIDs:       src.ClipIDs,
		opts:          opts,
		titleFallback: textutil.FirstNonEmpty(resCtx.Title, "Clip Script"),
		startTime:     start,
	}, r.log)
}

// computeSourceFingerprint builds a deterministic fingerprint from
// the source spec and resolved evidence.
func computeSourceFingerprint(src scriptpkg.SourceSpec, ev *scriptpkg.ClipEvidence) string {
	return BuildItemIdentity(scriptpkg.GenerationItemV2{
		Source: src,
	})
}
<<<<<<< Updated upstream:internal/application/scripts/source_resolver_clips.go
=======

// BuildItemIdentity constructs an identity string for a generation item.
// Phase 1b stub.
func BuildItemIdentity(item scriptpkg.GenerationItemV2) string {
	return ""
}
>>>>>>> Stashed changes:internal/application/scripts/usecase/source_resolver_clips.go

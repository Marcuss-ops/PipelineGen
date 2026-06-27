// Package scripts — source_resolver_clips.go resolves SourceClips
// sources into a ResolvedSource. It uses ClipSourceBuilder to fetch
// clips by ID and build context, then converts the result into
// typed ClipEvidence.
package scripts

import (
	"context"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

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
func (r *ClipsSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "clips source resolver: ClipSourceBuilder not configured",
		}
	}

	if len(src.ClipIDs) == 0 {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "clips source requires at least one clip_id",
		}
	}

	start := time.Now()

	opts := &ClipGenerationOptions{
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
		titleFallback: "Clip Script",
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



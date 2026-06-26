// Package scripts — source_resolver_clips.go resolves SourceClips
// sources into a ResolvedSource. It uses ClipSourceBuilder to fetch
// clips by ID and build context, then converts the result into
// typed ClipEvidence.
package scripts

import (
	"context"
	"fmt"
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
		Language:           "",
		Title:              "",
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
	}

	pack, plan, sourceText, err := r.clipBuilder.BuildClipContext(ctx, src.ClipIDs, opts)
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceClips,
			Query:       strings.Join(src.ClipIDs, ","),
			ResultCount: 0,
			Inner:       fmt.Errorf("clip context build failed: %w", err),
		}
	}

	// Build typed ClipEvidence from the pack.
	evidence := BuildClipEvidence(pack, sourceText)

	// Resolve title from plan.
	title := ""
	if plan != nil {
		title = plan.Title
	}
	if title == "" && evidence != nil && len(evidence.ClipIDs) > 0 {
		title = "Clip Script"
	}

	// Compute fingerprint using the new identity helper.
	fingerprint := computeSourceFingerprint(src, evidence)

	if r.log != nil {
		r.log.Info("clips source resolved",
			zap.Int("clip_ids", len(src.ClipIDs)),
			zap.Int("clips_found", evidence.ClipCount),
			zap.Int64("elapsed_ms", time.Since(start).Milliseconds()))
	}

	return &scriptpkg.ResolvedSource{
		Type:         scriptpkg.SourceClips,
		Topic:        title,
		Title:        title,
		SourceText:   sourceText,
		ClipEvidence: evidence,
		Fingerprint:  fingerprint,
	}, nil
}

// computeSourceFingerprint builds a deterministic fingerprint from
// the source spec and resolved evidence.
func computeSourceFingerprint(src scriptpkg.SourceSpec, ev *scriptpkg.ClipEvidence) string {
	return BuildItemIdentity(scriptpkg.GenerationItemV2{
		Source: src,
	})
}



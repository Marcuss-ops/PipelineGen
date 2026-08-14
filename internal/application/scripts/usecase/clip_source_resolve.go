// Package usecase — clip_source_resolve.go owns the clip-resolution
// domain of ClipSourceBuilder (PR-REFACTOR-P1-CYCLOMATIC extraction):
//
//   - typedClipResolverPort — the narrow resolution surface.
//   - resolveOneClip — the 2-phase resolution (ResolveByMediaAssetID →
//     ResolveByDriveFileID fallback) with a strongly-typed reason.
//   - resolveClipContextResult — the per-clip parallel worker that
//     threads drive-link + transcript checks into a clipContextRecord.
//   - clipContextRecord / clipContextResult / clipResolveReason — the
//     typed carriers consumed by BuildClipContext's orchestrator loop.
//
// Single-orchestrator-invariant: BuildClipContext (clip_source_builder.go)
// is the ONLY caller of resolveClipContextResult; this file exposes the
// resolution primitives, never a second orchestrator.
package usecase

import (
	"context"
	"errors"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// typedClipResolverPort is the narrow clip-resolution surface
// ClipSourceBuilder depends on.
type typedClipResolverPort interface {
	ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error)
	ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error)
}

type clipContextRecord struct {
	id           string
	clip         *asset.Asset
	transcript   string
	track        *asset.TextTrack
	metadataText string
}

type clipContextResult struct {
	record  clipContextRecord
	missing *scriptpkg.MissingClipID
	err     error
}

// clipResolveReason is the typed return value of resolveOneClip.
type clipResolveReason string

const (
	clipResolveOK       clipResolveReason = "ok"
	clipResolveNotFound clipResolveReason = "not_found"
)

func (c *ClipSourceBuilder) resolveOneClip(ctx context.Context, id string) (*asset.Asset, clipResolveReason) {
	clip, err := c.clipsRepo.ResolveByMediaAssetID(ctx, id)
	if err != nil && c.log != nil {
		c.log.Warn("clip source builder: failed to fetch clip by media asset id",
			zap.String("clip_id", id),
			zap.Error(err))
	}
	if clip == nil {
		list, driveErr := c.clipsRepo.ResolveByDriveFileID(ctx, id)
		if driveErr != nil {
			if c.log != nil {
				c.log.Warn("clip source builder: failed to fetch clip by drive file id",
					zap.String("clip_id", id),
					zap.Error(driveErr))
			}
			return nil, clipResolveNotFound
		}
		if len(list) > 0 {
			clip = list[0]
		}
	}
	if clip == nil {
		return nil, clipResolveNotFound
	}
	return clip, clipResolveOK
}

func (c *ClipSourceBuilder) resolveClipContextResult(
	ctx context.Context,
	id string,
	language string,
	requireDriveLink bool,
	allowTranscriptFallback bool,
	allowMetadataFallback bool,
	metadataText string,
) clipContextResult {
	clip, reason := c.resolveOneClip(ctx, id)
	switch reason {
	case clipResolveOK:
		// fall through to the drive-link / transcript checks below.
	case clipResolveNotFound:
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			},
		}
	default:
		if c.log != nil {
			c.log.Warn("clip source builder: unknown resolve reason", zap.String("clip_id", id), zap.String("reason", string(reason)))
		}
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonNotFound,
			},
		}
	}

	if requireDriveLink && clip.DriveLink() == "" {
		if c.log != nil {
			c.log.Warn("clip source builder: clip lacks drive link (missing — Issue #2 bucket)",
				zap.String("clip_id", id))
		}
		return clipContextResult{
			missing: &scriptpkg.MissingClipID{
				ClipID: id,
				Reason: scriptpkg.MissingClipReasonDriveNotFound,
			},
		}
	}

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
	// resolveTranscript is called EXACTLY ONCE per clip.
	// The signature is (string, *asset.TextTrack, error) —
	// transcript string first, resolved track second,
	// error third. The resolved transcript feeds both
	// the assembled source text (via appendClipSourceText)
	// and the per-clip ClipDetail.Transcript (via
	// appendClipDetail). The resolved *asset.TextTrack
	// feeds the 3 new fingerprint fields (via the
	// resolvedTracks accumulator + buildClipEvidence).
	transcript, track, resolveErr := c.resolveTranscript(ctx, clip.ID, language, clip)
	if resolveErr != nil {
		var notReady *ErrTextTrackNotReady
		if (allowTranscriptFallback || (allowMetadataFallback && (clipHasMetadataEvidence(clip) || strings.TrimSpace(metadataText) != ""))) && errors.As(resolveErr, &notReady) {
			// Explicit source_text is the only permitted fallback. Keep the
			// resolved clip record so its metadata remains available, but do
			// not fabricate transcript/evidence content.
			return clipContextResult{record: clipContextRecord{id: id, clip: clip, metadataText: metadataText}}
		}
		if c.log != nil {
			c.log.Warn("clip source builder: text track resolve failed",
				zap.String("clip_id", id),
				zap.String("language", language),
				zap.Error(resolveErr))
		}
		return clipContextResult{err: resolveErr}
	}

	return clipContextResult{
		record: clipContextRecord{
			id:           id,
			clip:         clip,
			transcript:   transcript,
			track:        track,
			metadataText: metadataText,
		},
	}
}

func metadataFallbackAllowed(opts *ClipGenerationOptions, clip *asset.Asset) bool {
	if !metadataFallbackEnabled(opts) {
		return false
	}
	return clipHasMetadataEvidence(clip) || strings.TrimSpace(opts.MetadataFallbackText) != ""
}

func metadataFallbackEnabled(opts *ClipGenerationOptions) bool {
	return opts != nil && opts.AllowMetadataFallback
}

func metadataFallbackText(opts *ClipGenerationOptions, clipID string) string {
	if opts == nil {
		return ""
	}
	if opts.MetadataFallbackByClipID != nil {
		return strings.TrimSpace(opts.MetadataFallbackByClipID[clipID])
	}
	return strings.TrimSpace(opts.MetadataFallbackText)
}

func clipHasMetadataEvidence(clip *asset.Asset) bool {
	if clip == nil {
		return false
	}
	return strings.TrimSpace(clip.SearchText) != "" ||
		strings.TrimSpace(clip.GetMetadataString("description")) != "" ||
		strings.TrimSpace(clip.GetMetadataString("visual_summary")) != ""
}

// transcriptFallbackAllowed permits a missing transcript only when the
// caller supplied explicit source text for the whole request or for the
// segment that owns this clip. The policy names are intentionally narrow:
// unknown policies remain strict and never enable a silent fallback.
func transcriptFallbackAllowed(opts *ClipGenerationOptions, clipID string) bool {
	if opts == nil || !transcriptPolicyAllowsSourceTextFallback(opts.TranscriptPolicy) {
		return false
	}
	if strings.TrimSpace(opts.SourceText) != "" {
		return true
	}
	for _, segment := range opts.Segments {
		if strings.TrimSpace(segment.SourceText) == "" {
			continue
		}
		for _, declaredID := range segment.ClipIDs {
			if strings.TrimSpace(declaredID) == strings.TrimSpace(clipID) {
				return true
			}
		}
	}
	return false
}

func transcriptPolicyAllowsSourceTextFallback(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", scriptpkg.TranscriptPolicyAuto, scriptpkg.TranscriptPolicyStrict, scriptpkg.TranscriptPolicySourceTextFallback:
		return true
	default:
		return false
	}
}

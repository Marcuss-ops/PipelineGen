// Package scripts — source_resolver_clips.go resolves SourceClips
// sources into a ResolvedSource. It uses ClipSourceBuilder to fetch
// clips by ID and build context, then converts the result into
// typed ClipEvidence.
package usecase

import (
	"context"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ptrutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ClipsSourceResolver resolves SourceClips (explicit clip IDs)
// into a ResolvedSource with ClipEvidence.
type ClipsSourceResolver struct {
	clipBuilder clipContextBuilder
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

	canonicalSegments := scriptpkg.CanonicalizeSegmentClipIDs(src, resCtx.Segments)
	requestedIDs := scriptpkg.CollectRequestedClipIDs(src, resCtx.Segments)
	if len(requestedIDs) == 0 {
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
		Language:         resCtx.Language,
		Title:            resCtx.Title,
		Tone:             resCtx.Tone,
		Model:            resCtx.Model,
		Style:            resCtx.Style,
		TargetWords:      resCtx.TargetWords,
		NumClips:         resCtx.NumClips,
		SegmentWords:     resCtx.SegmentWords,
		Segments:         canonicalSegments,
		SourceText:       src.SourceText,
		TranscriptPolicy: src.TranscriptPolicy,
		// Explicit source.type=clips payloads own their declared order;
		// never let a resolver-side ordering strategy reorder those IDs.
		OrderingStrategy:   "",
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
		// A clip summary/description is sufficient grounding evidence for
		// the explicit clips workflow. Transcript timing remains optional;
		// when a transcript is absent the canonical summary is retained and
		// no transcript is fabricated.
		AllowMetadataFallback: true,
		// P0 #3 (June 2026): DriveLink required only when caller
		// wants document or scene images.
		RequireDriveLink: resCtx.RequireDriveLink,
	}

	resolved, err := buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceClips,
		query:         strings.Join(requestedIDs, ","),
		clipIDs:       requestedIDs,
		opts:          opts,
		titleFallback: textutil.FirstNonEmpty(resCtx.Title, "Clip Script"),
		startTime:     start,
	}, r.log)
	if err != nil {
		return nil, err
	}
	if resolved != nil && resolved.ClipEvidence != nil && len(canonicalSegments) > 0 {
		resolved.ClipEvidence.SegmentEvidence = scriptpkg.BuildSegmentClipEvidence(canonicalSegments, resolved.ClipEvidence)
	}
	return resolved, nil
}

// BuildClipFingerprint computes a deterministic fingerprint from the
// fields of a SourceSpec and resolved ClipEvidence that ACTUALLY
// affect the generated script output.
//
// This function is a thin wrapper around the canonical
// script.BuildFingerprint. All fingerprint logic lives in the
// domain package; this wrapper exists only to preserve the existing
// usecase-package call sites.
func BuildClipFingerprint(src scriptpkg.SourceSpec, ev *scriptpkg.ClipEvidence) string {
	return scriptpkg.BuildFingerprint(scriptpkg.FingerprintInputFromSource(src, ev))
}

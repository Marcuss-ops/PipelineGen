// Package metadata — service enrichment methods.
//
// service_enrich.go owns the MetadataService enrichment methods:
// GenerateClipMetadata, FallbackMetadata, fallbackMetadata,
// DeriveFallbackSourceVersion, and EnrichClip.
// Extracted from service.go (July 2026, PR-YOUTUBE-METADATA-SPLIT).
package metadata

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

// GenerateClipMetadata is the canonical application-layer entry
// point the legacy MetadataService.GenerateClipMetadata stub
// delegated to. It calls the builder and returns the typed
// envelope; callers downstream of this method persist via
// the writer, NOT via direct repo calls.
//
// The (nil, err) return path is reserved for input-validation
// failures (empty ClipID). An Ollama outage is NOT a nil
// return — the builder's deterministic fallback produces a
// real CanonicalClipMetadata in that case (commit 4 spec).
func (s *MetadataService) GenerateClipMetadata(
	ctx context.Context,
	in youtubetypes.ClipMetadataInput,
) (youtubetypes.CanonicalClipMetadata, error) {
	if in.ClipID == "" {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("metadata.GenerateClipMetadata: ClipID is required")
	}
	out, err := s.builder.Build(ctx, in)
	if err != nil {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("metadata.GenerateClipMetadata: builder: %w", err)
	}
	if out.ClipID == "" || out.Summary == "" {
		return s.fallbackMetadata(in), nil
	}
	// Builders may return only semantic fields. Canonical provenance comes
	// from the request and must survive every enrichment implementation.
	out.SourceURL = in.SourceURL
	out.SourceTitle = in.SourceTitle
	out.SourceChannel = in.SourceChannel
	out.SourceProvider = in.SourceProvider
	out.VideoID = in.VideoID
	out.ClipStartSec = in.ClipStartSec
	out.ClipEndSec = in.ClipEndSec
	out.ClipDurationSec = in.ClipEndSec - in.ClipStartSec
	out.PolicyVersion = in.PolicyVersion
	out.DrivePath = in.DrivePath
	out.ContentHash = in.ContentHash
	if out.Title == "" {
		out.Title = in.Title
	}
	return out, nil
}

// FallbackMetadata is the deterministic non-Ollama path
// EXPORTED for the infrastructure package
// (internal/infrastructure/youtube/ollama_clip_metadata_builder.go)
// to call when the Ollama call fails / times-out / returns
// un-parseable JSON. Exposed so the concrete builder doesn't
// duplicate the formula body — the spec's verdict is "ONE
// canonical algorithm, two callers".
func FallbackMetadata(in youtubetypes.ClipMetadataInput) youtubetypes.CanonicalClipMetadata {
	summary := in.Title
	if len(summary) > 240 {
		summary = summary[:240]
	}
	transcript := in.Transcript
	transcriptWordCount := CountWords(transcript)
	score := CalculateQualityScore(
		transcriptWordCount,
		in.ClipDuration,
		0, 0, 0,
	)
	transcriptPath := ""
	sponsor := IsSponsorSegment(transcript)
	group := in.Group
	if group == "" {
		group = "general"
	}
	return youtubetypes.CanonicalClipMetadata{
		ClipID:           in.ClipID,
		AssetID:          in.ClipID,
		Summary:          summary,
		Topics:           nil,
		Speakers:         nil,
		MentionedPeople:  nil,
		QualityScore:     score,
		SponsorSegment:   sponsor,
		TranscriptPath:   transcriptPath,
		SourceURL:        in.SourceURL,
		SourceTitle:      in.SourceTitle,
		SourceChannel:    in.SourceChannel,
		SourceProvider:   in.SourceProvider,
		VideoID:          in.VideoID,
		ClipStartSec:     in.ClipStartSec,
		ClipEndSec:       in.ClipEndSec,
		ClipDurationSec:  in.ClipEndSec - in.ClipStartSec,
		PolicyVersion:    in.PolicyVersion,
		DrivePath:        in.DrivePath,
		ContentHash:      in.ContentHash,
		NormalizedGroup:  group,
		SourceVersion:    DeriveFallbackSourceVersion(in.ClipID, in.Transcript, score),
		JobID:            "",
		Hook:             in.Hook,
		SearchVisibility: in.SearchVisibility,
	}
}

// fallbackMetadata is the service-instance variant that
// stamps the service's group + jobID. Used by
// GenerateClipMetadata (when the builder returns a degraded
// envelope) and by EnrichClip (when the builder fallback
// path runs).
func (s *MetadataService) fallbackMetadata(in youtubetypes.ClipMetadataInput) youtubetypes.CanonicalClipMetadata {
	out := FallbackMetadata(in)
	if s.jobID != "" {
		out.JobID = s.jobID
	}
	if in.Group == "" && s.group != "" {
		out.NormalizedGroup = s.group
	}
	if len(in.Topics) > 0 {
		out.Topics = in.Topics
	}
	if len(in.Speakers) > 0 {
		out.Speakers = in.Speakers
	}
	if len(in.MentionedPeople) > 0 {
		out.MentionedPeople = in.MentionedPeople
	}
	if in.Hook != "" {
		out.Hook = in.Hook
	}
	if in.SearchVisibility != "" {
		out.SearchVisibility = in.SearchVisibility
	}
	out.SourceTitle = in.SourceTitle
	out.SourceChannel = in.SourceChannel
	out.SourceProvider = in.SourceProvider
	out.VideoID = in.VideoID
	out.ClipStartSec = in.ClipStartSec
	out.ClipEndSec = in.ClipEndSec
	out.ClipDurationSec = in.ClipEndSec - in.ClipStartSec
	out.PolicyVersion = in.PolicyVersion
	out.DrivePath = in.DrivePath
	out.ContentHash = in.ContentHash
	return out
}

// DeriveFallbackSourceVersion is the EXPORTED form of the
// deterministic source-version fingerprint. The infra
// package uses this for the Ollama path's sourceVersion
// (so production + fallback produce stable, comparable
// fingerprints).
func DeriveFallbackSourceVersion(clipID, transcript string, score float64) string {
	if clipID == "" {
		return ""
	}
	payload := clipID + "|" + transcript + "|" + fmt.Sprintf("%.4f", score)
	return Sha256Short(payload)
}

// EnrichClip is the application-layer orchestration the
// pre-Commit-4 usecase.MetadataService stubbed. It calls
// the builder + persists via the writer. Returns the
// typed envelope on success, an error on persistence
// failure (the caller's job classifier inspects via
// errors.Is / errors.As).
func (s *MetadataService) EnrichClip(
	ctx context.Context,
	in youtubetypes.ClipMetadataInput,
) (youtubetypes.CanonicalClipMetadata, error) {
	if in.ClipID == "" {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("metadata.EnrichClip: ClipID is required")
	}
	md, err := s.GenerateClipMetadata(ctx, in)
	if err != nil {
		return youtubetypes.CanonicalClipMetadata{}, fmt.Errorf("metadata.EnrichClip: build: %w", err)
	}
	if err := s.writer.UpdateClipMetadataAndRequestIndex(ctx, md.ClipID, md); err != nil {
		s.log.Warn("metadata.EnrichClip: writer failed",
			zap.String("clip_id", md.ClipID),
			zap.Error(err))
		return md, fmt.Errorf("metadata.EnrichClip: writer: %w", err)
	}
	return md, nil
}

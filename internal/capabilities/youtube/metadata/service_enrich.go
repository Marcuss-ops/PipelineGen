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

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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
	// Request-provided semantic fields must survive every enrichment
	// implementation. Caller-supplied values WIN when non-empty; the LLM
	// (or deterministic fallback) only fills gaps. The builder is
	// transcript-only, so it must never overwrite the caller's summary /
	// tags / topics / speakers / mentioned-people with derived values
	// (godlike/06 request-provided-survives contract).
	if in.Description != "" {
		out.Description = in.Description
	}
	if in.Title != "" {
		out.Title = in.Title
	}
	if in.Summary != "" {
		out.Summary = in.Summary
	}
	if len(in.Tags) > 0 {
		out.Tags = in.Tags
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
	return out, nil
}

// FallbackMetadata is the deterministic non-Ollama path
// EXPORTED for the infrastructure package
// (internal/platform/youtube/ollama_clip_metadata_builder.go)
// to call when the Ollama call fails / times-out / returns
// un-parseable JSON. Exposed so the concrete builder doesn't
// duplicate the formula body — the spec's verdict is "ONE
// canonical algorithm, two callers".
func FallbackMetadata(in youtubetypes.ClipMetadataInput) youtubetypes.CanonicalClipMetadata {
	summary := in.Summary
	if summary == "" {
		summary = in.Title
	}
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
		Description:      in.Description,
		Topics:           append([]string(nil), in.Topics...),
		Speakers:         append([]string(nil), in.Speakers...),
		MentionedPeople:  append([]string(nil), in.MentionedPeople...),
		Tags:             append([]string(nil), in.Tags...),
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
	if in.Summary != "" {
		out.Summary = in.Summary
	}
	if len(in.Tags) > 0 {
		out.Tags = in.Tags
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

// CanonicalClipEnrichment is the PURE output of the metadata analyzer.
// It is the semantic snapshot a caller converts into a
// persistence.CommitRequest / mediacommit.CommitMediaAssetRequest. The
// analyzer itself never writes media_assets — persistence is the caller's
// (or the canonical asset committer's) responsibility.
type CanonicalClipEnrichment struct {
	AssetID         string
	Description     string
	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
	QualityScore    float64
	Tags            []string
	SearchText      string
	TextTracks      []detail.TextTrack
}

// Compile-time assertion: MetadataService is a pure MetadataAnalyzer.
var _ MetadataAnalyzer = (*MetadataService)(nil)

// AnalyzeClip is the PURE metadata-analysis step. It builds the canonical
// metadata (builder + deterministic fallback) and projects it into a
// CanonicalClipEnrichment WITHOUT writing media_assets or emitting an
// outbox event. This is the analyzer half of the former EnrichClip; the
// write is deliberately separate so callers can converge it into the
// single canonical asset commit.
func (s *MetadataService) AnalyzeClip(
	ctx context.Context,
	in youtubetypes.ClipMetadataInput,
) (CanonicalClipEnrichment, error) {
	if in.ClipID == "" {
		return CanonicalClipEnrichment{}, fmt.Errorf("metadata.AnalyzeClip: ClipID is required")
	}
	md, err := s.GenerateClipMetadata(ctx, in)
	if err != nil {
		return CanonicalClipEnrichment{}, fmt.Errorf("metadata.AnalyzeClip: build: %w", err)
	}
	return ComposeCanonicalClipEnrichment(md), nil
}

// ComposeCanonicalClipEnrichment projects a CanonicalClipMetadata into the
// analyzer's CanonicalClipEnrichment. It owns the composition of the
// semantic snapshot (tags, search text, and the transcript text track) so
// callers never hand-assemble those fields from the raw metadata envelope.
func ComposeCanonicalClipEnrichment(md youtubetypes.CanonicalClipMetadata) CanonicalClipEnrichment {
	assetID := md.AssetID
	if assetID == "" {
		assetID = md.ClipID
	}
	searchText := md.EmbeddingText
	if searchText == "" {
		searchText = BuildFallbackSearchText(md.CleanTitle, md.Summary, md.Topics, md.CleanTranscript)
	}
	enrichment := CanonicalClipEnrichment{
		AssetID:         assetID,
		Description:     md.Description,
		Summary:         md.Summary,
		Topics:          md.Topics,
		Speakers:        md.Speakers,
		MentionedPeople: md.MentionedPeople,
		Hook:            md.Hook,
		QualityScore:    md.QualityScore,
		Tags:            md.Tags,
		SearchText:      searchText,
	}
	if md.CleanTranscript != "" {
		enrichment.TextTracks = []detail.TextTrack{
			{
				AssetID:      assetID,
				LanguageCode: md.OriginalLanguage,
				TextKind:     detail.TextTrackTranscript,
				TextContent:  md.CleanTranscript,
				SourceType:   detail.TextSourceProvided,
				IsOriginal:   true,
				Status:       detail.TextTrackReady,
			},
		}
	}
	return enrichment
}

// EnrichClip is the legacy orchestration that analyzes the clip AND
// persists via the ClipMetadataWriter in one call. It remains for
// backward compatibility with the pre-analyzer Step 10 path; new callers
// should use AnalyzeClip and converge the enrichment into the canonical
// asset commit instead of performing a second metadata-only write.
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
	if s.writer == nil {
		return md, fmt.Errorf("metadata.EnrichClip: ClipMetadataWriter is not wired (use AnalyzeClip for pure analysis)")
	}
	if err := s.writer.UpdateClipMetadataAndRequestIndex(ctx, md.ClipID, md); err != nil {
		s.log.Warn("metadata.EnrichClip: writer failed",
			zap.String("clip_id", md.ClipID),
			zap.Error(err))
		return md, fmt.Errorf("metadata.EnrichClip: writer: %w", err)
	}
	return md, nil
}

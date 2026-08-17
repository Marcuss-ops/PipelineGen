// Package metadata is the canonical home for the YouTube clip metadata
// enrichment capability (PR-C-YouTube-Cutover Commit 4/6, June 2026).
//
// The package owns:
//   - The ClipMetadataBuilder typed port (builder.go) — the boundary
//     between application-layer orchestration and the concrete
//     infrastructure builder (Ollama + deterministic fallback).
//   - The MetadataService application service (service.go) — wires the
//     builder + the ClipMetadataWriter port + the helper functions
//     (isSponsorSegment, calculateQualityScore, parseClipTimestamps,
//     BuildFallbackSearchText) into the canonical EnrichClip /
//     GenerateClipMetadata surface.
//
// Why a separate sub-package: the previous `usecase.MetadataService`
// had stubbed methods (GenerateClipMetadata returned nil, isSponsorSegment
// returned false, calculateQualityScore returned 0.5) — those stubs are
// the P1 #15 + #16 holes this commit closes. The new package ships the
// production implementation behind the typed port so callers
// (process_segment.go, EnrichClip) read the canonical shapes
// (dto.CanonicalClipMetadata + dto.ClipMetadataInput) without
// depending on the concrete Ollama client or the SQLite writer.
//
// Pattern 0 (PR1.7, June 2026): structural port carries signature-bearing
// method set so compile-time `var _ metadata.ClipMetadataBuilder =
// (*OllamaClipMetadataBuilder)(nil)` assertions catch drift.
package metadata

import (
	"context"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

// ClipMetadataBuilder is the typed port the application layer uses
// to produce a CanonicalClipMetadata for a single clip. The concrete
// implementation lives in infrastructure (the Ollama-driven builder
// + deterministic fallback).
//
// Build contract:
//
//   - MUST return a non-nil CanonicalClipMetadata on success. The
//     builder is fail-closed: an unrecoverable Ollama outage is
//     NOT a reason to return (nil, nil). The deterministic fallback
//     path is always available (clip_duration + transcript_word_count
//   - semantic coverage produce a real quality_score in [0.0, 1.0]),
//     so Build only returns an error when the input itself is
//     invalid (empty ClipID).
//   - MUST populate QualityScore in the [0.0, 1.0] range (the
//     quality_score column is REAL NOT NULL DEFAULT 0.0 in
//     media_assets). The fallback formula uses the constants in
//     youtubetypes.QualityScore* to keep the weighted sum bounded.
//   - MUST populate SponsorSegment with a real boolean (no nil-pointer
//     defaults). The fallback uses the regex from
//     isSponsorSegmentRegex.MatchString(transcript).
//   - MUST populate SourceVersion with a non-empty deterministic
//     fingerprint. The writer hashes SourceVersion into the
//     outbox event_key; an empty value would produce a
//     deterministic-but-misleading key that masks real content
//     changes (mirrors the godlike/07 envelope.go fail-closed
//     posture).
type ClipMetadataBuilder interface {
	Build(ctx context.Context, in youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error)
}

// MetadataAnalyzer is the typed port for the PURE metadata-analysis step.
// It produces a CanonicalClipEnrichment (the semantic snapshot: description,
// summary, topics, speakers, mentioned people, hook, quality score, tags,
// search text, text tracks) WITHOUT writing media_assets. The write is a
// separate concern owned by the caller (the canonical asset commit), not by
// the analyzer.
//
// AnalyzeClip contract:
//
//   - MUST be side-effect free with respect to media_assets: it calls the
//     builder + deterministic fallback and returns the enrichment, never
//     persisting a row or emitting an outbox event.
//   - MUST return a non-empty AssetID when the input ClipID is non-empty.
//     The deterministic fallback path always produces a real enrichment
//     (godlike/07 NO-FAKE-AVAILABILITY), so AnalyzeClip only errors on
//     invalid input (empty ClipID) or a builder-level failure.
type MetadataAnalyzer interface {
	AnalyzeClip(ctx context.Context, in youtubetypes.ClipMetadataInput) (CanonicalClipEnrichment, error)
}

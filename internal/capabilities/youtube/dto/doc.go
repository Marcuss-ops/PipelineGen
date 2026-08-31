// Package dto holds pure-data transfer shapes that cross the
// youtube-package boundary.
//
// ── Surviving metadata types (CLIPS-META A6+A7, July 2026) ──────────
//
// CanonicalClipMetadata — single canonical output type (23 fields).
//
//	Absorbed ClipRichMetadata (tags + semantic embedding fields)
//	and ClipMetadata (was a separate type pre-Azione 1).
//
// ClipMetadataInput — single canonical builder input type (11 fields).
//
//	Consumed by ClipMetadataBuilder.Build() and MetadataService
//	methods (GenerateClipMetadata, EnrichClip, FallbackMetadata).
//	BuildClipMetadataInput (usecase/segments_service.go) was removed
//	as dead code — zero production callers, different purpose
//	(lifecycle.FinalizeInput construction, not metadata enrichment).
//
// ClipMetadataFile — on-disk JSON serialization DTO for per-clip
//
//	metadata.json files written by WriteClipMetadataFile.
//
// Quality score constants: QualityScoreTranscriptWeight (0.40),
//
//	QualityScoreDurationWeight (0.40), QualityScoreSemanticWeight
//	(0.20), QualityScoreSponsorPenalty (0.20).
//
// ── Non-metadata types ─────────────────────────────────────────────
//
// ExtractRequest/Response, DownloaderMetadata, Segment,
// TopicSearchRequest, ClipAsset (writer-bound entity).
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): BREAKING RENAME from
// youtube/types/ → youtube/dto/. The 26 internal importers using the
// yttypes alias migrate to this package in the same PR. No back-compat
// alias is provided — callers MUST update their import path.
//
// Import discipline:
//   - MUST NOT import other youtube/ sub-packages.
//   - MAY import internal/kernel/asset (canonical asset types).
//   - All exported types are JSON-tagged where applicable.
package dto

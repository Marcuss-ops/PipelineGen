// Package dto holds pure-data transfer shapes that cross the
// youtube-package boundary.
//
// CLIPS-META-2026-07-04 (Azione 1): CanonicalClipMetadata is the SINGLE
// canonical clip metadata output type. ClipRichMetadata and ClipMetadata
// are zero-copy type aliases (= CanonicalClipMetadata) for backward
// compatibility. ClipMetadataInput is the canonical builder input type.
// ClipMetadataFile is a separate on-disk JSON serialization DTO.
//
// Non-metadata types: ExtractRequest/Response, DownloaderMetadata,
// Segment, TopicSearchRequest, ClipAsset (writer-bound entity).
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): BREAKING RENAME from
// youtube/types/ → youtube/dto/. The 26 internal importers using the
// yttypes alias migrate to this package in the same PR. No back-compat
// alias is provided — callers MUST update their import path.
//
// Import discipline:
//   - MUST NOT import other youtube/ sub-packages.
//   - MAY import internal/domain/asset (canonical asset types).
//   - All exported types are JSON-tagged where applicable.
package dto

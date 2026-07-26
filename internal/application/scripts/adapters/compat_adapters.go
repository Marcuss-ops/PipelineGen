// Package adapters — compat_adapters.go (PR-noop-adapters-purge, 2026-07-25).
//
// Defines the canonical typed ports EntityExtractor + MetadataGenerator
// that postprocessors consume at composition time, plus the typed-fail
// adapters used when a real backend is not wired.
//
// PR-noop-adapters-purge (2026-07-25, P0 absolute — wave
// CLEANUP-PRIORITY-1-5-2026-07-25) replaces the noop adapters that
// silently returned empty EntityResult{} / nil []VideoMetadata on
// every request (godlike/07 NO-FAKE-AVAILABILITY violation). The
// new unavailable*Adapter returns a TYPED SENTINEL on every call
// so callers can detect the unwired condition via errors.Is and
// fail loudly at the operator dashboard rather than shipping a
// successful-but-incomplete postprocessor result.
//
// PR 3 (June 2026): the typed ports were introduced to replace the
// legacy PostGenFunc callback + GenerationSpec bridge. Previously
// entities and metadata processors consumed an opaque any;
// the PR 3 typed ports enable compile-time audits.
//
// godlike/06 SSOT: the typed ports EntityExtractor +
// MetadataGenerator live ONLY here (= the canonical owner). The
// pre-PR duplicate definitions in dto/compat_types.go were
// retired in this PR (same signatures, two owners violated the
// one-canonical-owner-per-fact invariant).
package adapters

import (
	"context"
	"errors"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Typed error sentinels (PR-noop-adapters-purge, 2026-07-25) ────────────

// ErrEntityExtractorUnavailable is returned by the unavailable entity
// extraction typed-fail adapter when no real backend is wired at
// composition time. Callers probe via errors.Is and surface a typed
// "backend unavailable" diagnostic in the operator dashboard.
//
// godlike/07 NO-FAKE-AVAILABILITY: the pre-PR noop adapter returned
// EntityResult{} with nil error, masking the unwired condition as
// a successful (but empty) postprocessor result. The new typed-fail
// adapter refuses to perform silent-success and propagates the
// unwired condition to the caller as a typed error.
var ErrEntityExtractorUnavailable = errors.New("adapters: EntityExtractor backend unavailable (fail-closed per godlike/07 — wire a real backend or remove the entities postprocessor)")

// ErrMetadataGeneratorUnavailable mirrors ErrEntityExtractorUnavailable
// for the metadata generation typed-fail adapter. See the doc comment
// above for rationale.
var ErrMetadataGeneratorUnavailable = errors.New("adapters: MetadataGenerator backend unavailable (fail-closed per godlike/07 — wire a real backend or remove the metadata postprocessor)")

// ── ArtlistClipMatch: result type for clip-search postprocessor ──────────

// ArtlistClipMatch pairs an artlist phrase with matching clip results.
// This is the adapters-layer wire shape (parallel to
// usecase.ScriptArtlistClipSuggestion which lives in the usecase
// package and cannot be imported here due to circular-dependency
// constraints).
type ArtlistClipMatch struct {
	Phrase           string   `json:"phrase"`
	ClipNames        []string `json:"clip_names,omitempty"`
	ClipDriveLinks   []string `json:"clip_drive_links,omitempty"`
	FolderLink       string   `json:"folder_link,omitempty"`
	FolderName       string   `json:"folder_name,omitempty"`
	FolderID         string   `json:"folder_id,omitempty"`
	TranslationError string   `json:"translation_error,omitempty"`
}

// ArtlistClipSearcher is the canonical port for searching Artlist
// clips from extracted entity phrases. Processors (ClipSearchProcessor)
// consume an ArtlistClipSearcher at composition time and dispatch
// (title, phrases) → []ArtlistClipMatch.
//
// godlike/06 SSOT: the port is declared ONLY here. No other package
// may redefine ArtlistClipSearcher.
type ArtlistClipSearcher interface {
	SearchClips(ctx context.Context, title string, phrases []string) []ArtlistClipMatch
}

// InternetImageSearchRequest carries the canonical per-segment query
// for web-image retrieval.
type InternetImageSearchRequest struct {
	SegmentID string
	Query     string
	Entity    string
	TextHash  string
	Language  string
	Limit     int
	Provider  string
}

// InternetImageSearcher is the canonical port for internet-image
// retrieval. The processor consumes this interface and never knows
// about HTTP APIs or routing internals.
type InternetImageSearcher interface {
	SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

// VidRushMetrics is the bounded observability port for the per-segment
// pipeline. Job/item/text values stay in structured logs, not metric labels.
type VidRushMetrics = scriptmetrics.VidRushMetrics

// ── Typed ports (PR 3 — June 2026) ────────────────────────────────────────

// EntityExtractor is the canonical port for entity extraction.
// Processors (EntitiesProcessor) consume an EntityExtractor at
// composition time and dispatch EntityExtractionRequest → EntityResult.
//
// godlike/06 SSOT: the port is declared ONLY here. No other package
// may redefine EntityExtractor.
type EntityExtractor interface {
	ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error)
}

// MetadataGenerator is the canonical port for video metadata
// generation. Processors (MetadataProcessor) consume a
// MetadataGenerator at composition time and dispatch
// MetadataGenerationRequest → []VideoMetadata.
//
// godlike/06 SSOT: see EntityExtractor above.
type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error)
}

// ── Typed-fail adapters (PR-noop-adapters-purge, 2026-07-25) ──────────────
//
// The unavailable*Adapter is the canonical fail-closed adapter for
// creator runtime (no backend per the creator-runtime package
// contract: no DB, no Qdrant, no Scheduler — see
// internal/app/creator_runtime.go) AND for any composition site
// where the real backend has not yet been wired. Every call returns
// the typed sentinel; the caller observes the unwired condition as
// a typed error rather than a silent-success empty result.
//
// The pre-PR noopEntityExtractionAdapter + noopMetadataGenerationAdapter
// were physically removed in this PR — godlike/07 MINIMUM-BLAST-RADIUS
// disallows the construction pattern (silent-success) that wrapped
// every request in a successful empty payload.

// unavailableEntityExtractionAdapter is the canonical fail-closed
// implementation of EntityExtractor. Returns ErrEntityExtractorUnavailable
// on every ExtractEntities call.
type unavailableEntityExtractionAdapter struct{}

// NewUnavailableEntityExtractionAdapter returns the canonical fail-closed
// EntityExtractor. The returned adapter is safe for concurrent use and
// carries no state.
func NewUnavailableEntityExtractionAdapter() EntityExtractor {
	return unavailableEntityExtractionAdapter{}
}

// ExtractEntities implements EntityExtractor. Returns nil EntityResult
// + ErrEntityExtractorUnavailable on every call (fail-closed).
func (unavailableEntityExtractionAdapter) ExtractEntities(_ context.Context, _ scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	return nil, ErrEntityExtractorUnavailable
}

// unavailableMetadataGenerationAdapter is the canonical fail-closed
// implementation of MetadataGenerator. Returns
// ErrMetadataGeneratorUnavailable on every GenerateMetadata call.
type unavailableMetadataGenerationAdapter struct{}

// NewUnavailableMetadataGenerationAdapter returns the canonical
// fail-closed MetadataGenerator. The returned adapter is safe for
// concurrent use and carries no state.
func NewUnavailableMetadataGenerationAdapter() MetadataGenerator {
	return unavailableMetadataGenerationAdapter{}
}

// GenerateMetadata implements MetadataGenerator. Returns nil
// []VideoMetadata + ErrMetadataGeneratorUnavailable on every call
// (fail-closed).
func (unavailableMetadataGenerationAdapter) GenerateMetadata(_ context.Context, _ scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error) {
	return nil, ErrMetadataGeneratorUnavailable
}

// ── ArtlistClipSearcher: no unavailable adapter (PR-LEGACY-UNAVAILABLE-CLIPSEARCH, 2026-07-10) ──
//
// The unavailableArtlistClipSearcher was retired per godlike/07
// NO-FAKE-AVAILABILITY: the adapter returned nil on every SearchClips
// call — a silent-success that was indistinguishable from "no clips
// found." Post PR-LEGACY-UNAVAILABLE-CLIPSEARCH, the composition root
// SKIPS ClipSearchProcessor registration entirely when OllamaTranslator
// is nil (matching the TranslationProcessor pattern). The
// ArtlistClipSearcher port + ArtlistClipMatch type are preserved —
// still consumed by the real adapter when the backend IS wired.

// Package metadata — service.go: canonical MetadataService.
//
// PR-C-YouTube-Cutover Commit 4/6 (June 2026, P1 #15 + #16):
// materialises the previously-stubbed MetadataService helpers and
// wires the new ClipMetadataBuilder + ClipMetadataWriter ports. The
// service is the canonical owner of:
//
//   - GenerateClipMetadata  — calls ClipMetadataBuilder.Build, falls
//     back to a deterministic scoreboard when the builder returns
//     an error or the Ollama client is nil.
//   - BuildFallbackSearchText — concatenates title + summary +
//     topics + transcript_excerpt into a 1KB-bounded
//     semantic-search surface used when Ollama is unavailable.
//   - IsSponsorSegment (regex) — replaces the legacy keyword
//     substring match with a canonical regex per the user's
//     spec (`\bsponsored by\b|\badvertisement\b|\bprovided by\b|...`).
//   - CalculateQualityScore — real formula using
//     (clip_duration, transcript_word_count, semantic_coverage)
//     with a fixed -0.20 penalty for sponsor segments. The
//     deterministic fallback in the Ollama builder uses the
//     same formula so production + fallback produce the same
//     range. NOT the legacy 0.5 default.
//   - parseClipTimestamps — regex-based HH:MM:SS / MM:SS parser
//     used by WriteClipMetadataFile to recover the
//     (startSec, endSec) tuple from a canonical yt_<vid>_<s>_<e>_*
//     clipID. Replaces the legacy underscore-split heuristic.
//   - AnalyzeClip — PURE analysis that returns a
//     CanonicalClipEnrichment without writing media_assets.
//   - EnrichClip — legacy orchestration that
//     calls the builder + writes via ClipMetadataWriter
//     (NOT direct assetRepo.Upsert — the verdict's P1 #15
//     fail-closed posture on raw repo writes). New callers
//     prefer AnalyzeClip + the canonical asset commit.
//
// // PR-YOUTUBE-METADATA-SPLIT (July 2026): decomposed the original
// 837-LoC monolithic service.go into single-purpose files per
// AGENTS.md Pattern 5:
//
//   - service.go               — slim orchestrator: MetadataDeps +
//     MetadataService struct + NewMetadataService
//   - service_enrich.go        — GenerateClipMetadata + FallbackMetadata +
//     fallbackMetadata + DeriveFallbackSourceVersion +
//     EnrichClip
//   - quality_scoring.go       — IsSponsorSegment + CalculateQualityScore +
//     CountWords + Sha256Short
//   - metadata_extraction.go   — BuildFallbackSearchText +
//     parseClipTimestamps + atoiOrZero
//   - enrichment.go            — WriteClipMetadataFile +
//     ym*Canonical helpers
package metadata

import (
	"fmt"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// MetadataDeps is the typed dependency set the canonical
// MetadataService needs. Max 8 fields per AGENTS.md Pattern 5.
//
// ClipMetadataWriter is required — the service fails closed at
// ctor time when it is nil so a wiring gap surfaces at startup,
// not at first EnrichClip call. The previous direct-assetRepo-
// -Upsert path is intentionally NOT supported (P1 #15).
type MetadataDeps struct {
	Builder  ClipMetadataBuilder
	Writer   youtubeports.ClipMetadataWriter
	Logger   *zap.Logger
	JobID    string // optional; stamped into outbox payload
	JobGroup string // normalized_group, stamped on writes
}

// MetadataService is the canonical YouTube clip metadata
// enrichment service. Methods are pure (no global state,
// no hidden deps) so tests can drive the surface via the
// typed-port fakes without patching the production concrete.
type MetadataService struct {
	builder ClipMetadataBuilder
	writer  youtubeports.ClipMetadataWriter
	log     *zap.Logger
	jobID   string
	group   string
}

// NewMetadataAnalyzer constructs the PURE metadata analyzer. Only the
// Builder is required; the Writer may be nil because AnalyzeClip never
// writes media_assets. Logger may be nil — the analyzer falls back to
// zap.NewNop.
func NewMetadataAnalyzer(deps MetadataDeps) (*MetadataService, error) {
	if deps.Builder == nil {
		return nil, fmt.Errorf("metadata.NewMetadataAnalyzer: ClipMetadataBuilder is required (P1 #15 fail-closed — the previous direct-assetRepo path is removed)")
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &MetadataService{
		builder: deps.Builder,
		writer:  deps.Writer,
		log:     log,
		jobID:   deps.JobID,
		group:   deps.JobGroup,
	}, nil
}

// NewMetadataService constructs the legacy service that analyzes AND
// persists via the ClipMetadataWriter. The Writer is required (P1 #15
// fail-closed); the Builder is required (the service has nothing to do
// without it). Prefer NewMetadataAnalyzer for callers that only need the
// pure enrichment.
func NewMetadataService(deps MetadataDeps) (*MetadataService, error) {
	svc, err := NewMetadataAnalyzer(deps)
	if err != nil {
		return nil, err
	}
	if deps.Writer == nil {
		return nil, fmt.Errorf("metadata.NewMetadataService: ClipMetadataWriter is required (P1 #15 fail-closed — direct assetRepo.Upsert is removed)")
	}
	return svc, nil
}

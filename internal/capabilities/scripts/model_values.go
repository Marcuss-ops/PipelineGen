// Package scriptgeneration — model_values.go: the primitive and reference value
// types of the pure domain model (ZERO I/O, ZERO external dependencies).
//
// Language, Source, ClipReference (+ its canonical duration resolution),
// AudioReference and DocumentReference — the shapes the aggregates in model.go
// embed. The render/audio projections live in model_render.go.
//
// Extracted 2026-09-12 from model.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Value types ─────────────────────────────────────────────────────

// Language is an ISO 639-1 two-letter code (e.g. "en", "es").
type Language string

// Source describes where the generation input comes from.
type Source struct {
	Type               SourceType                  `json:"type"`
	Topic              string                      `json:"topic,omitempty"`
	SourceText         string                      `json:"source_text,omitempty"`
	ArtlistKeywords    []string                    `json:"artlist_keywords,omitempty"`
	ClipIDs            []string                    `json:"clip_ids,omitempty"`
	NumClips           int                         `json:"num_clips,omitempty"`
	Query              string                      `json:"query,omitempty"`
	MaxClips           int                         `json:"max_clips,omitempty"`
	MinCoverage        float64                     `json:"min_coverage,omitempty"`
	MinQualityScore    *float64                    `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int                        `json:"min_transcript_words,omitempty"`
	Guidelines         string                      `json:"guidelines,omitempty"`
	TranscriptPolicy   string                      `json:"transcript_policy,omitempty"`
	OrderingStrategy   string                      `json:"ordering_strategy,omitempty"`
	GroundingPolicy    string                      `json:"grounding_policy,omitempty"`
	FallbackPolicy     string                      `json:"fallback_policy,omitempty"`
	ForceRefresh       bool                        `json:"force_refresh,omitempty"`
	Search             bool                        `json:"search,omitempty"`
	AllowTextOnly      bool                        `json:"allow_text_only,omitempty"`
	SourceFilter       string                      `json:"source_filter,omitempty"`
	MediaTypeFilter    string                      `json:"media_type_filter,omitempty"`
	CachePolicy        scriptpkg.SourceCachePolicy `json:"cache,omitempty"`
	Research           scriptpkg.ResearchPolicy    `json:"research,omitempty"`
}

// SourceType is the canonical generation-source enum, aliased to the kernel
// script.SourceType so the kernel remains the single source of truth for the
// enum contract (including SourceResearch). The constants below re-export the
// kernel values so existing callers compile without churn while no drift is
// possible — a value the local model did not declare ("research") previously
// slipped through a permissive string cast; the alias makes that impossible.
type SourceType = scriptpkg.SourceType

const (
	SourceText     = scriptpkg.SourceText
	SourceClips    = scriptpkg.SourceClips
	SourceCatalog  = scriptpkg.SourceCatalog
	SourceSearch   = scriptpkg.SourceSearch
	SourceCurate   = scriptpkg.SourceCurate
	SourceResearch = scriptpkg.SourceResearch
)

// ClipReference identifies a single media clip.
type ClipReference struct {
	ID        string `json:"id"`
	SourceID  string `json:"source_id,omitempty"`
	Title     string `json:"title,omitempty"`
	DriveLink string `json:"drive_link,omitempty"`
	// Duration is the legacy wire field carrying the asset total duration in
	// float seconds. DEPRECATED for internal computation — read DurationUS
	// (integer microseconds) + DurationSource (provenance) instead.
	Duration float64 `json:"duration,omitempty"` // seconds (legacy)
	// DurationUS is the canonical total duration of the complete source asset
	// in integer microseconds, resolved once at the media boundary.
	DurationUS int64 `json:"duration_us,omitempty"`
	// DurationSource is the canonical provenance of DurationUS
	// (probe / provider_metadata / unknown).
	DurationSource kernelasset.DurationSource `json:"duration_source,omitempty"`
	AudioAssetID   string                     `json:"audio_asset_id,omitempty"`
	AudioPath      string                     `json:"audio_path,omitempty"`
	Path           string                     `json:"path,omitempty"`
	SHA256         string                     `json:"sha256,omitempty"`
	FrameCount     int64                      `json:"frame_count,omitempty"`
	SourceInMS     int64                      `json:"source_in_ms,omitempty"`
	SourceOutMS    int64                      `json:"source_out_ms,omitempty"`
	// Subject identity of the clip, threaded from the canonical asset
	// metadata (ClipSemanticMetadata). The scene↔clip identity gate uses
	// the union of Speakers + MentionedPeople + Subject to certify that a
	// clip actually features the subject its scene narrates (the
	// "Tom Holland / Adam Sandler" class of error).
	Speakers        []string `json:"speakers,omitempty"`
	MentionedPeople []string `json:"mentioned_people,omitempty"`
	Subject         string   `json:"subject,omitempty"`
}

// AssetDuration resolves the canonical total duration of this clip with
// provenance, applying the contract invariants (positive known values,
// explicit unknown — never a fabricated 0). DurationUS + DurationSource are
// the canonical fields; a legacy caller that only populated Duration (no
// source) is mapped to provider_metadata — the pre-contract value came from
// asset metadata, not a fresh local probe — so it stays known while remaining
// distinguishable from a real probe measurement.
func (c *ClipReference) AssetDuration() kernelasset.AssetDuration {
	if c == nil {
		return kernelasset.UnknownDuration()
	}
	us := c.DurationUS
	if us <= 0 && c.Duration > 0 {
		us = checkedFloatSeconds(c.Duration, "clip duration")
	}
	if us <= 0 {
		return kernelasset.UnknownDuration()
	}
	switch c.DurationSource {
	case kernelasset.DurationProbe:
		return kernelasset.ProbedDuration(us)
	case kernelasset.DurationProvider:
		return kernelasset.ProviderDuration(us)
	case kernelasset.DurationUnknown:
		return kernelasset.UnknownDuration()
	default:
		// Legacy unprovenanced value: known but not a fresh probe.
		return kernelasset.ProviderDuration(us)
	}
}

// AudioReference identifies a generated voiceover audio asset.
type AudioReference struct {
	ID       string  `json:"id"`
	URL      string  `json:"url,omitempty"`
	FilePath string  `json:"file_path,omitempty"`
	Duration float64 `json:"duration,omitempty"` // seconds
	// Timing is the canonical word-level timing captured in the SAME
	// synthesis stream that produced the audio (the Edge WordBoundary
	// payload). It is the SSOT from which the phrase→timestamp projection
	// (GenerateResult.PhraseTimings) is derived. Nil when timing capture
	// was not requested or unavailable for this voiceover.
	Timing *capabilityaudio.SpeechTimingArtifact `json:"timing,omitempty"`
	// TimingBundle carries the published timing bundle references
	// (timing.json SSOT + optional SRT/VTT links + hashes) for this
	// voiceover language. It is the document-facing summary; the word-level
	// SSOT stays in Timing (never inlined). Nil when no timing bundle was
	// published (timing disabled / unavailable / failed).
	TimingBundle *scriptpkg.VoiceoverTimingBinding `json:"timing_bundle,omitempty"`
	// Cached reports that this voiceover was reused from the cross-run
	// SQLite fingerprint cache: zero TTS provider calls, uploads, or
	// finalize work were spent for this item. It is the runner-facing
	// signal behind AudioMetrics.VoiceoverDBCacheHits.
	Cached bool `json:"cached,omitempty"`
}

// DocumentReference identifies a published Google Doc.
type DocumentReference struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

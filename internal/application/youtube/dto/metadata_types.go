// Package dto — metadata_types.go: canonical types for the YouTube clip
// metadata enrichment pipeline (PR-C-YouTube-Cutover Commit 4/6, P1 #15 + #16).
//
// These types are the shared currency between:
//   - The ClipMetadataBuilder port (metadata/builder.go) — the
//     application-layer port the use case consumes.
//   - The ClipMetadataWriter port (ports/ports.go) — the writer port the
//     MetadataService calls to persist enriched metadata.
//   - The concrete infra adapters (sqlite/assets/clip_metadata_writer.go,
//     infra/youtube/ollama_clip_metadata_builder.go).
//
// Prior to this file, CanonicalClipMetadata and ClipMetadataInput were
// referenced across the codebase but never defined — the types were part
// of the unreconciled Commit 4/6 cutover that broke compilation.

package dto

// ── Quality Score Weights ────────────────────────────────────────────
//
// These constants define the weighted-sum decomposition of the canonical
// quality-score formula (calculateQualityScore in metadata/service.go).
// Total = transcript * 0.4 + duration * 0.4 + semantic * 0.2, clamped
// to [0.0, 1.0].

const (
	QualityScoreTranscriptWeight  = 0.40
	QualityScoreDurationWeight    = 0.40
	QualityScoreSemanticWeight    = 0.20
	QualityScoreSponsorPenalty    = 0.20 // subtracted from raw score when SponsorSegment=true
)

// ── ClipMetadataInput ────────────────────────────────────────────────

// ClipMetadataInput is the typed input envelope the ClipMetadataBuilder
// port consumes. It carries the raw per-clip data the builder needs to
// produce a CanonicalClipMetadata — title, transcript, duration, and
// source metadata.
//
// Builders (Ollama + deterministic fallback) read ALL fields; a nil or
// empty field is handled as a zero-value by the builder (no downstream
// panic).
type ClipMetadataInput struct {
	// ClipID is the canonical yt_<videoID>_<startSec>_<endSec>_<policy>
	// identifier. Required — empty ClipID is a builder-level error.
	ClipID string

	// Title is the clip's display title (segment name or video title).
	// Used by the fallback path as the Summary when the builder returns
	// a degraded envelope.
	Title string

	// Transcript is the Whisper or yt-dlp subtitle text for the clip.
	// Used by the quality-score formula (transcript_word_count) and the
	// sponsor-segment regex.
	Transcript string

	// ClipDuration is the clip duration in seconds (endSec - startSec).
	// Used by the quality-score formula's duration sweet-spot sub-score.
	ClipDuration int

	// SourceURL is the original YouTube video URL. Stamped into the
	// metadata JSON and the outbox event payload for operator audit.
	SourceURL string

	// Group is the canonical normalized_group (e.g. "general",
	// "tutorial", "vlog"). When empty, the metadata service falls back
	// to "general".
	Group string

	// Hook is the LLM-discovered hook phrase (Commit 4 upstream signal).
	// Preserved verbatim in the deterministic fallback path.
	Hook string

	// SearchVisibility is the LLM-assigned visibility tier
	// (Commit 4 upstream signal). Preserved verbatim in the
	// deterministic fallback path.
	SearchVisibility string

	// Topics is the LLM-discovered topic list (Commit 4 upstream
	// signal). Preserved verbatim.
	Topics []string

	// Speakers is the LLM-discovered speaker list (Commit 4 upstream
	// signal). Preserved verbatim.
	Speakers []string

	// MentionedPeople is the LLM-discovered mentioned-people list
	// (Commit 4 upstream signal). Preserved verbatim.
	MentionedPeople []string
}

// ── CanonicalClipMetadata ────────────────────────────────────────────

// CanonicalClipMetadata is the single canonical clip metadata type.
// It bundles ALL metadata fields the ClipMetadataWriter persists into
// media_assets.metadata_json AND the outbox event payload.
//
// CLIPS-META-2026-07-04 (Azione 1): expanded from 14 to 23 fields,
// absorbing ClipRichMetadata's tag-level enrichment + semantic
// embedding fields. This is now the SINGLE source of truth for
// clip metadata output — ClipRichMetadata is a zero-copy alias;
// ClipMetadata is retired.
type CanonicalClipMetadata struct {
	// ClipID is the canonical clip identifier. Must equal the clipID
	// passed to the writer; a mismatch is a caller bug that fails the
	// write (ClipMetadataWriterAdapter validates this).
	ClipID string

	// AssetID is the canonical media_assets row identifier. Set to
	// ClipID by the metadata builder (the two are the same in the
	// canonical schema).
	AssetID string

	// Summary is the 240-char-max clip summary. Populated either by
	// the Ollama builder or by the deterministic fallback (truncated
	// title).
	Summary string

	// Topics is the LLM-discovered topic list for semantic search
	// indexing. Nil/empty is valid (no topics discovered).
	Topics []string

	// Speakers is the LLM-discovered speaker list. Nil/empty is valid.
	Speakers []string

	// MentionedPeople is the LLM-discovered mentioned-people list.
	// Nil/empty is valid.
	MentionedPeople []string

	// QualityScore is the weighted composite score in [0.0, 1.0]
	// computed from transcript coverage, duration sweet-spot, and
	// semantic metadata richness. The Ollama builder and the
	// deterministic fallback use the same formula so scores are
	// comparable across both paths.
	QualityScore float64

	// SponsorSegment is true when the clip transcript matches the
	// sponsored-content regex (IsSponsorSegment). The indexing layer
	// applies a -0.20 penalty on top of the raw QualityScore when
	// this flag is set.
	SponsorSegment bool

	// TranscriptPath is the local filesystem path to the transcript
	// text file (VTT/TXT) produced by Whisper or yt-dlp subtitle
	// extraction. May be empty when no transcript was extracted.
	TranscriptPath string

	// SourceURL is the original YouTube video URL. Stamped for
	// operator audit.
	SourceURL string

	// NormalizedGroup is the canonical group name for folder routing
	// (e.g. "general", "tutorial"). Defaults to "general" when the
	// input didn't carry a group.
	NormalizedGroup string

	// SourceVersion is the deterministic content fingerprint used as
	// the outbox event_key suffix. Computed by the builder as either
	// the file hash or Md5(clipID + transcript + qualityScore).
	// Empty SourceVersion is fail-closed at the writer level.
	SourceVersion string

	// JobID is the optional job identifier stamped into the outbox
	// payload. Populated by the service-instance field when set;
	// empty when the builder runs outside a job context (e.g. the
	// Ollama builder's deterministic fallback).
	JobID string

	// Hook is the LLM-discovered hook phrase (Commit 4 upstream
	// signal). When non-empty, stamped into metadata_json AND the
	// outbox event payload. When empty, omitted entirely from
	// the json_set chain (no empty semantic marker persisted).
	Hook string

	// SearchVisibility is the LLM-assigned visibility tier (Commit 4
	// upstream signal). Same nil-vs-empty contract as Hook.
	SearchVisibility string

	// ── Absorbed from ClipRichMetadata (CLIPS-META-2026-07-04) ──────

	// SourceTags carries source/channel-level tags extracted from
	// the video title + description.
	SourceTags []string

	// ClipTags carries clip-level topic tags.
	ClipTags []string

	// SearchKeywords carries SEO-oriented keyword phrases.
	SearchKeywords []string

	// People is the union of Speakers + MentionedPeople (deduplicated).
	People []string

	// CleanTitle is the canonical cleaned clip title.
	CleanTitle string

	// ShortTitle is the shortened (≤4 word) display title.
	ShortTitle string

	// CleanTranscript is the cleaned, normalized transcript text.
	CleanTranscript string

	// EmbeddingText is the structured text block fed to the embedding
	// model for semantic search indexing.
	EmbeddingText string

	// Tags is the merged-deduplicated union of SourceTags + ClipTags +
	// SearchKeywords + Topics + Speakers + MentionedPeople.
	Tags []string
}

// Package script — source_spec.go defines the canonical source-agnostic
// contract for script-generation input. Every generation item declares
// exactly one SourceType; the resolver for that type produces a
// ResolvedSource that feeds the engine.
//
// No durable field uses any, any, or map[string]any.
package script

import "strings"

// SourceType enumerates the canonical input sources for script generation.
type SourceType string

const (
	// SourceText means the caller supplied inline text (topic + optional
	// source_text + optional guidelines).
	SourceText SourceType = "text"

	// SourceClips means the caller supplied explicit clip IDs.
	SourceClips SourceType = "clips"

	// SourceCatalog means the caller wants to search the local media
	// catalog for matching clips.
	SourceCatalog SourceType = "catalog"

	// SourceSearch means the caller wants a full semantic search
	// (Qdrant + reranker) for matching assets.
	SourceSearch SourceType = "search"

	// SourceCurate means the caller wants the curation pipeline:
	// union of semantic search + HintClipIDs + ClipSourceBuilder.
	// Produced by MediaCurator.Curate → CurateSourceResolver.
	SourceCurate SourceType = "curate"
)

// Canonical grounding policies for clip-aware generation.
const (
	// GroundingPolicyClipsPrimary means the output must stay anchored
	// to clip evidence; no unsupported claims are allowed.
	GroundingPolicyClipsPrimary = "clips_primary"
	// GroundingPolicySourcePrimary means source_text is the main source
	// and clips are used only as visual support.
	GroundingPolicySourcePrimary = "source_primary"
	// GroundingPolicyBalanced means clip evidence and source_text have
	// equal weight.
	GroundingPolicyBalanced = "balanced"
)

// Canonical fallback policies for clip-aware generation.
const (
	// FallbackPolicyStrict means the job fails when clip-native
	// planning cannot produce scenes from the provided clips.
	FallbackPolicyStrict = "strict"
	// FallbackPolicyAllowProse means a prose fallback is permitted
	// when clip-native planning fails; the result must carry
	// SUCCEEDED_WITH_WARNINGS and declare the fallback.
	FallbackPolicyAllowProse = "allow_prose"
)

// SourceSpec declares where script-generation input comes from.
// Exactly one source type must be active; the resolver validates
// that the corresponding fields are populated.
type SourceSpec struct {
	// Type is the canonical source selector.
	Type SourceType `json:"type"`

	// ── Text source ───────────────────────────────────────────────────
	Topic      string `json:"topic,omitempty"`
	SourceText string `json:"source_text,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clips source ──────────────────────────────────────────────────
	ClipIDs      []string `json:"clip_ids,omitempty"`
	IntroClipIDs []string `json:"intro_clip_ids,omitempty"`
	NumClips     int      `json:"num_clips,omitempty"`

	// ── Catalog / Search source ───────────────────────────────────────
	Query              string   `json:"query,omitempty"`
	MaxClips           int      `json:"max_clips,omitempty"`
	MinCoverage        float64  `json:"min_coverage,omitempty"`
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`

	// ── Clip pipeline options ─────────────────────────────────────────
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`
	// GroundingPolicy is the canonical clip-grounding policy used
	// when building the model-facing prompt. It is part of the
	// generation fingerprint so policy changes invalidate cached
	// results.
	GroundingPolicy string `json:"grounding_policy,omitempty"`
	// FallbackPolicy is the canonical fallback policy for clip-aware
	// generation. It controls whether the pipeline is allowed to fall
	// back to prose when clip-native planning cannot produce scenes.
	FallbackPolicy string `json:"fallback_policy,omitempty"`
	ForceRefresh   bool   `json:"force_refresh,omitempty"`

	// ── Curation source (SourceCurate) ────────────────────────────────
	// Search enables the semantic search leg (Qdrant via ClipSearchPort).
	Search bool `json:"search,omitempty"`
	// AllowTextOnly permits the legacy text-only fallback when no
	// clips resolve from either search or HintClipIDs.
	AllowTextOnly bool `json:"allow_text_only,omitempty"`
	// SourceFilter restricts semantic search to a specific source.
	SourceFilter string `json:"source_filter,omitempty"`
	// MediaTypeFilter restricts semantic search to a specific media type.
	MediaTypeFilter string `json:"media_type_filter,omitempty"`
}

// IsText returns true when the source type is text.
func (s *SourceSpec) IsText() bool { return s.Type == SourceText }

// IsClips returns true when the source type is clips (explicit IDs).
func (s *SourceSpec) IsClips() bool { return s.Type == SourceClips }

// IsCatalog returns true when the source type is catalog.
func (s *SourceSpec) IsCatalog() bool { return s.Type == SourceCatalog }

// IsSearch returns true when the source type is search.
func (s *SourceSpec) IsSearch() bool { return s.Type == SourceSearch }

// IsCurate returns true when the source type is curate.
func (s *SourceSpec) IsCurate() bool { return s.Type == SourceCurate }

// HasClipIDs returns true when explicit clip IDs are present,
// regardless of source type.
func (s *SourceSpec) HasClipIDs() bool { return len(s.ClipIDs) > 0 }

// ── Resolved source ────────────────────────────────────────────────────

// ── SourceResolutionContext ────────────────────────────────────────────────

// SourceResolutionContext is the canonical resolution-time context
// passed alongside SourceSpec through SourceRegistry.Resolve. It
// carries item-level traits (target language, tone, model, style,
// target word count) that a resolver needs to produce a ResolvedSource
// matching the operator's intent.
//
// Rationale (PR 4, June 2026): previously the curate resolver hijacked
// SourceSpec.Guidelines as a stand-in for ClipGenerationOptions
// Language — a bug because Guidelines is style instructions, not a
// language code. SourceResolutionContext makes the boundary explicit:
// language = real target language; style = style instructions; tone =
// delivery tone; model = engine model; target words = script length
// budget. The resolver receives BOTH SourceSpec (source-side
// instructions: Query, ClipIDs, SourceFilter, ...) AND
// SourceResolutionContext (operator-side traits) so neither field leaks
// into the other.
//
// Construction: built in generate_one_usecase.go::Execute from the
// resolved plan right before registry.Resolve. Resolvers that don't
// need these fields (e.g. pure-text resolver) ignore them.
type SourceResolutionContext struct {
	// ItemID is the canonical generation item identifier. Resolvers use
	// it for logging correlation; not propagated into ResolvedSource.
	ItemID string `json:"item_id,omitempty"`

	// Title is the resolved document/video title.
	Title string `json:"title,omitempty"`

	// Language is the canonical target output language (ISO 639-1).
	// Resolvers map this verbatim into ClipGenerationOptions.Language.
	// PR 4 fix: previously the curate resolver used src.Guidelines here.
	Language string `json:"language,omitempty"`

	// Tone is the voice/delivery tone (e.g. "informative", "dramatic").
	// Mapped into ClipGenerationOptions.Tone.
	Tone string `json:"tone,omitempty"`

	// Model is the engine model identifier (e.g. "llama3:8b").
	// Mapped into ClipGenerationOptions.Model.
	Model string `json:"model,omitempty"`

	// Style is the editorial style instructions (free-form text).
	// Distinct from SourceSpec.Guidelines: SourceSpec.Guidelines is
	// retained only for pure-text editorial overrides and is ignored by
	// the curate flow per PR 4.
	Style string `json:"style,omitempty"`

	// TargetWords is the target script word count (e.g. 800 for a
	// 5-minute voiceover). Mapped into ClipGenerationOptions.TargetWords.
	TargetWords int `json:"target_words,omitempty"`

	// NumClips is the requested number of clips/scenes for clip-based
	// generation. When zero, the resolver may use all available clips.
	NumClips int `json:"num_clips,omitempty"`

	// SegmentWords is the desired approximate word budget per segment.
	SegmentWords int `json:"segment_words,omitempty"`

	// SegmentTopics is an ordered list of topics to cover per segment.
	SegmentTopics []string `json:"segment_topics,omitempty"`

	// Segments is the per-block payload forwarded from
	// item.ScriptParams into the resolver and ClipGenerationOptions.
	// Resolver threads whichever the caller chose; mutex with
	// SegmentTopics is enforced upstream at the validator layer.
	Segments []ScriptSegment `json:"segments,omitempty"`

	// RequireDriveLink tells the clip resolver whether clips MUST have
	// a Drive link to be included in the resolved set. When false
	// (the caller only wants text generation — no document, no scene
	// images), clips without Drive links are still accepted because
	// only transcript + metadata are needed.
	//
	// P0 #3 (June 2026): computed from item.Output.GenerateDocument ||
	// item.Output.GenerateSceneImages in buildResolutionContext.
	RequireDriveLink bool `json:"-"`
}

// ResolvedSource is the output of a SourceResolver. It carries the
// canonical text + evidence that the engine consumes. Every resolver
// (text, clips, catalog, search) produces the same shape so the
// engine never branches on source type.
type ResolvedSource struct {
	// Type echoes the SourceType that produced this resolution.
	Type SourceType `json:"type"`

	// Topic is the resolved topic (may be derived from clip titles,
	// catalog results, or the original query).
	Topic string `json:"topic"`

	// Title is the resolved document/video title.
	Title string `json:"title"`

	// SourceText is the canonical text fed to the engine.
	// For text sources it's the original topic+source_text.
	// For clip/catalog/search sources it's the assembled clip
	// evidence text.
	SourceText string `json:"source_text"`

	// Language is the resolved language (ISO 639-1).
	Language string `json:"language,omitempty"`

	// ClipEvidence holds the assembled clip context when the source
	// involved clip resolution. Nil for pure text sources.
	ClipEvidence *ClipEvidence `json:"clip_evidence,omitempty"`

	// SearchResults holds the raw search results when the source
	// involved semantic or catalog search. Nil otherwise.
	SearchResults []SearchResultItem `json:"search_results,omitempty"`

	// GroundingPolicy is the clip-grounding policy used when the
	// source involved clips. It is part of the generation fingerprint
	// so policy changes invalidate cached results.
	GroundingPolicy string `json:"grounding_policy,omitempty"`

	// Fingerprint is a deterministic hash of the resolved source
	// inputs, used for memory-gate caching and idempotency.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ClipEvidence is the assembled clip context produced by a clip
// resolver. It is the canonical shape passed from any clip-based
// source (clips, catalog, search) to the engine.
//
// Issue #2 (June 2026): the contract now distinguishes three clip
// populations instead of the previously-ambiguous single
// "ClipIDs" (which conflated "resolved with transcript" with
// "renderable into Drive-link-requiring surfaces"). The resolution
// pipeline populates:
//
//   - AcceptedClipIDs: clips resolved into the clips table AND
//     carrying usable transcript text. Drives prompt construction
//     (engine grounding), the SpecScene validator's allow-list,
//     the clip-bindings processor, and any consumer that needs
//     the textual context. The cardinality is independent of
//     RequireDriveLink: text-only generation (RequireDriveLink=
//     false) keeps clips without DriveLink here; document/scene-
//     image generation (RequireDriveLink=true) filters them to
//     MissingClipIDs instead.
//
//   - RenderableClipIDs: subset of AcceptedClipIDs that
//     ADDITIONALLY carry a non-empty DriveLink. Drives consumers
//     that need to embed the asset (document body, image
//     generation, voiceover reference). Always a subset (or empty)
//     of AcceptedClipIDs; never larger.
//
//   - MissingClipIDs: requested IDs that did NOT resolve into
//     usable evidence. Includes both lookup failures
//     (MissingClipReasonNotFound) and Drive-link-missing-when-
//     required infrastructure failures
//     (MissingClipReasonDriveNotFound). Distinct from Excluded
//     (which is a post-resolution quality step): MISSING records
//     lookup outcomes; EXCLUDED records filter outcomes.
//
//   - Excluded: post-resolution quality filters (transcript too
//     short, quality score below threshold, policy drop). NEVER
//     carries a Drive-not-found reason — that distinction is the
//     Issue #2 contract fix (the old implementation appended
//     MissingClipReasonDriveNotFound to Excluded, conflating
//     infrastructure failures with quality filters; resolved
//     post-Issue #2 by ClipSourceBuilder's bucket-flip).
type ClipEvidence struct {
	// AcceptedClipIDs is the final list of clip IDs effectively
	// resolved by the lookup chain AND carrying usable transcript
	// text. Issue #2 (June 2026): renamed from the previously-
	// ambiguous "ClipIDs" to make the contract explicit — this
	// field is the prompt-grounding set, not the rendering set.
	// JSON key "accepted_clip_ids" (was "clip_ids", breaking per
	// AGENTS.md posture — internal struct, clean break).
	AcceptedClipIDs []string `json:"accepted_clip_ids"`

	// RenderableClipIDs is the subset of AcceptedClipIDs that
	// additionally carry a non-empty DriveLink — i.e. clips that
	// can be embedded into the document body, fed as image
	// prompts, or referenced from voiceover. Always a subset (or
	// empty when RequireDriveLink=false and resolved clips lacked
	// DriveLinks). Added in Issue #2 (June 2026).
	RenderableClipIDs []string `json:"renderable_clip_ids,omitempty"`

	// ClipCount is len(AcceptedClipIDs). Kept as a typed field
	// for monitoring and operator dashboards so a missing/empty
	// array cannot accidentally appear as 0-without-explanation.
	ClipCount int `json:"clip_count"`

	// AssembledText is the concatenated transcript/description text
	// ready for legacy provenance and compatibility consumers.
	AssembledText string `json:"assembled_text,omitempty"`

	// NarrativeText is the model-facing projection of the resolved
	// clip evidence. It contains only narration-safe evidence blocks
	// and excludes technical markers such as clip IDs, Drive links,
	// tags and source URLs.
	NarrativeText string `json:"narrative_text,omitempty"`

	// DriveLinks maps clip_id → Drive URL for downstream rendering.
	DriveLinks map[string]string `json:"drive_links,omitempty"`

	// ClipNames maps clip_id → clip title/description.
	ClipNames map[string]string `json:"clip_names,omitempty"`

	// ExcludedClipIDs lists clips that were filtered out (quality,
	// transcript length) with reasons.
	Excluded []ExcludedClip `json:"excluded,omitempty"`

	// MissingClipIDs lists requested clip IDs that could not be
	// resolved into Drive-backed clip evidence (PR 5, June 2026).
	// nil when all requested IDs resolved. Each entry carries an
	// ID + structured reason so an operator dashboard can surface
	// "X client-asked clips, Y resolved, Z with broken Drive link,
	// W not found at all". Distinct from Excluded (which is a
	// post-resolution quality/filtering step): MissingClipIDs
	// records lookup outcomes; Excluded records filter outcomes.
	MissingClipIDs []MissingClipID `json:"missing_clip_ids,omitempty"`

	// ClipTranscriptHashes is the ordered list of SHA-256 hashes of
	// the transcript text for each accepted clip. It is part of the
	// generation fingerprint so transcript changes invalidate cached
	// results. The order matches AcceptedClipIDs.
	ClipTranscriptHashes []string `json:"clip_transcript_hashes,omitempty"`

	// ClipDetails holds per-clip evidence used to build scenes
	// directly from clip-native sources. It is populated by the
	// clip source builder and consumed by the scene binder when
	// the model does not emit a structured scene breakdown.
	ClipDetails map[string]ClipDetail `json:"clip_details,omitempty"`

	// LanguageCode is the BCP-47 code of the resolved text track
	// the video pipeline read from `asset_text_tracks` for the
	// caller's target language. PR-PY-CLIPS-CORRETTE-TRADOTTE
	// Fase 4 (July 2026): added so the generation fingerprint
	// can evolve when the resolved language changes (e.g. a
	// backfill populates the Italian track and the next
	// `script.generate` call resolves to "it" instead of "en").
	// Empty when the video pipeline ran in legacy metadata_json
	// fallback mode (pre-Fase 4) or when no READY track was found.
	LanguageCode string `json:"language_code,omitempty"`

	// TextTrackVersion is the source_version of the resolved
	// text track (e.g. "v1.0", or a model-version-derived
	// string). PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
	// added so the generation fingerprint evolves when the
	// translation re-derives (a re-translation bumps
	// source_version). Distinct from the per-row
	// `TextTrack.SourceVersion` field on the domain model:
	// this is the resolved value carried by the evidence.
	TextTrackVersion string `json:"text_track_version,omitempty"`

	// TranscriptHash is the per-row text_hash of the resolved
	// text track (canonical SHA-256 of the text_content). PR-PY-
	// CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): added so the
	// generation fingerprint evolves when the text_content
	// changes (a re-translation rewrites the row, bumps the
	// hash, invalidates the cached generation result). This is
	// distinct from the per-clip ClipTranscriptHashes slice:
	// this single field is the canonical "what version of the
	// resolved language did we read" hash.
	TranscriptHash string `json:"transcript_hash,omitempty"`
}

// ModelSourceText returns the clip evidence projection that is safe
// to send to the model. It intentionally fails closed: if NarrativeText
// is absent, the caller gets an empty string instead of the legacy
// technical text.
func (e *ClipEvidence) ModelSourceText() string {
	if e == nil {
		return ""
	}
	if text := strings.TrimSpace(e.NarrativeText); text != "" {
		return text
	}
	return ""
}

// ClipDetail carries the primary evidence for a single accepted
// clip. It is the canonical source of truth for clip-native scene
// construction (transcription, timestamps, metadata).
type ClipDetail struct {
	// Name is the human-readable clip title or filename.
	Name string `json:"name,omitempty"`
	// Description is the clip description or search text.
	Description string `json:"description,omitempty"`
	// Transcript is the canonical transcript excerpt for the clip.
	Transcript string `json:"transcript,omitempty"`
	// Tags are the clip tags.
	Tags []string `json:"tags,omitempty"`
	// StartMs is the optional clip start offset in milliseconds.
	StartMs int64 `json:"start_ms,omitempty"`
	// EndMs is the optional clip end offset in milliseconds.
	EndMs int64 `json:"end_ms,omitempty"`
	// DriveLink is the Google Drive URL for the clip.
	DriveLink string `json:"drive_link,omitempty"`
}

// ModelClipView is the model-facing projection of a clip. It strips
// away technical locators and keeps only evidence that can change the
// voiceover choice.
type ModelClipView struct {
	Ref           string `json:"ref"`
	Description   string `json:"description,omitempty"`
	VisualSummary string `json:"visual_summary,omitempty"`
	Transcript    string `json:"transcript,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
}

// ExcludedClip records a clip that was filtered out during resolution
// and the reason why.
type ExcludedClip struct {
	ClipID string `json:"clip_id"`
	Reason string `json:"reason"`
}

// MissingClipID records a requested clip ID that could not be
// resolved into Drive-backed clip evidence (PR 5, June 2026).
// Reason values are bounded by MissingClipReasonNotFound and
// MissingClipReasonDriveNotFound — any other string is treated
// as the empty reason (consumers should ignore it for dashboards).
//
// Distinct from ExcludedClip: this struct captures LOOKUP
// outcomes (the ID didn't make it through the resolver's
// GetClip / GetByDriveFileID chain), while ExcludedClip captures
// POST-RESOLUTION quality/filter outcomes (the ID resolved but
// was dropped by the resolver's quality gate).
type MissingClipID struct {
	ClipID string `json:"clip_id"`
	Reason string `json:"reason"`
}

// Canonical reasons for MissingClipID.Reason (PR 5, June 2026).
// Adding a new reason requires a deprecation record per
// architecture/godlike/07_ZERO_LEGACY_POLICY.md before it
// can ship in production.
const (
	// MissingClipReasonNotFound means neither GetClip nor
	// GetByDriveFileID returned a usable row for the requested ID.
	// Caller asked for an ID that is not present in the clips
	// table at all (typo, deleted asset, wrong source).
	MissingClipReasonNotFound = "not_found"

	// MissingClipReasonDriveNotFound means the requested ID
	// resolved to a clips-table row BUT the row's DriveLink
	// metadata is empty (the asset exists locally with no Drive
	// backing, or the Drive file was orphaned). The clip is
	// therefore present but unrenderable via Drive, and is
	// excluded from the resolved set by cli's DriveLink-empty
	// filter.
	MissingClipReasonDriveNotFound = "drivenotfound"
)

// SearchResultItem is a single search result returned by a catalog
// or semantic search resolver.
type SearchResultItem struct {
	ClipID    string  `json:"clip_id"`
	AssetID   string  `json:"asset_id,omitempty"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
	DriveLink string  `json:"drive_link,omitempty"`
}

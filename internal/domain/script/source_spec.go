// Package script — source_spec.go defines the canonical source-agnostic
// contract for script-generation input. Every generation item declares
// exactly one SourceType; the resolver for that type produces a
// ResolvedSource that feeds the engine.
//
// No durable field uses interface{}, any, or map[string]any.
package script

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
	ForceRefresh     bool   `json:"force_refresh,omitempty"`

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

	// Fingerprint is a deterministic hash of the resolved source
	// inputs, used for memory-gate caching and idempotency.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ClipEvidence is the assembled clip context produced by a clip
// resolver. It is the canonical shape passed from any clip-based
// source (clips, catalog, search) to the engine.
type ClipEvidence struct {
	// ClipIDs is the final list of clip IDs effectively resolved by
	// the lookup chain (GetClip → fallback to GetByDriveFileID).
	// PR 5 (June 2026): only IDs whose target clip has BOTH a row
	// in the clips table AND a non-empty DriveLink populate this
	// field. Requested-but-unresolved IDs surface in MissingClipIDs
	// (below) instead, with a structured reason. Downstream
	// consumers (engine grounding, spec-scene validator,
	// clip-bindings processor, HTML rendering) read this set as
	// the canonical "scene input" and bind only to it.
	ClipIDs []string `json:"clip_ids"`

	// ClipCount is len(ClipIDs). Kept as a typed field for
	// monitoring and operator dashboards so a missing/empty array
	// cannot accidentally appear as 0-without-explanation.
	ClipCount int `json:"clip_count"`

	// AssembledText is the concatenated transcript/description text
	// ready for the engine prompt.
	AssembledText string `json:"assembled_text,omitempty"`

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

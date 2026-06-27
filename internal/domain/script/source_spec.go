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
	ClipIDs  []string `json:"clip_ids,omitempty"`
	NumClips int      `json:"num_clips,omitempty"`

	// ── Catalog / Search source ───────────────────────────────────────
	Query            string  `json:"query,omitempty"`
	MaxClips         int     `json:"max_clips,omitempty"`
	MinCoverage      float64 `json:"min_coverage,omitempty"`
	MinQualityScore  *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int   `json:"min_transcript_words,omitempty"`

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
	// ClipIDs is the final list of clip IDs used.
	ClipIDs []string `json:"clip_ids"`

	// ClipCount is len(ClipIDs).
	ClipCount int `json:"clip_count"`

	// AssembledText is the concatenated transcript/description text
	// ready for the engine prompt.
	AssembledText string `json:"assembled_text,omitempty"`

	// DriveLinks maps clip_id → Drive URL for downstream rendering.
	DriveLinks map[string]string `json:"drive_links,omitempty"`

	// ExcludedClipIDs lists clips that were filtered out (quality,
	// transcript length) with reasons.
	Excluded []ExcludedClip `json:"excluded,omitempty"`
}

// ExcludedClip records a clip that was filtered out during resolution
// and the reason why.
type ExcludedClip struct {
	ClipID string `json:"clip_id"`
	Reason string `json:"reason"`
}

// SearchResultItem is a single search result returned by a catalog
// or semantic search resolver.
type SearchResultItem struct {
	ClipID    string  `json:"clip_id"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
	DriveLink string  `json:"drive_link,omitempty"`
}

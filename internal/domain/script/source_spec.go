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
}

// IsText returns true when the source type is text.
func (s *SourceSpec) IsText() bool { return s.Type == SourceText }

// IsClips returns true when the source type is clips (explicit IDs).
func (s *SourceSpec) IsClips() bool { return s.Type == SourceClips }

// IsCatalog returns true when the source type is catalog.
func (s *SourceSpec) IsCatalog() bool { return s.Type == SourceCatalog }

// IsSearch returns true when the source type is search.
func (s *SourceSpec) IsSearch() bool { return s.Type == SourceSearch }

// HasClipIDs returns true when explicit clip IDs are present,
// regardless of source type.
func (s *SourceSpec) HasClipIDs() bool { return len(s.ClipIDs) > 0 }

// ── Resolved source ────────────────────────────────────────────────────

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

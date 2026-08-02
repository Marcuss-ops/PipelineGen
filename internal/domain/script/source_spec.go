// Package script defines the canonical source-agnostic contracts used by
// script generation.
package script

import (
	"fmt"
	"strings"
)

// SourceType enumerates the canonical input sources for script generation.
type SourceType string

const (
	SourceText     SourceType = "text"
	SourceClips    SourceType = "clips"
	SourceCatalog  SourceType = "catalog"
	SourceSearch   SourceType = "search"
	SourceCurate   SourceType = "curate"
	SourceResearch SourceType = "research"
)

const (
	GroundingPolicyClipsPrimary  = "clips_primary"
	GroundingPolicySourcePrimary = "source_primary"
	GroundingPolicyBalanced      = "balanced"
)

const (
	FallbackPolicyStrict     = "strict"
	FallbackPolicyAllowProse = "allow_prose"
)

// Source cache policy modes.
const (
	SourceCacheModeDisabled       = "disabled"
	SourceCacheModeCacheOnly      = "cache_only"
	SourceCacheModePreferCache    = "prefer_cache"
	SourceCacheModeRefreshIfStale = "refresh_if_stale"
	SourceCacheModeForceRefresh   = "force_refresh"
)

// SourceCachePolicy controls how the per-topic source text cache is
// used during the prepare phase. It is independent from the script
// output cache (gemmamemory).
type SourceCachePolicy struct {
	// Mode is one of disabled, cache_only, prefer_cache,
	// refresh_if_stale, force_refresh.
	Mode string `json:"mode,omitempty"`

	// TTLHours is the cache entry lifetime. Zero or negative values
	// fall back to the repository default (7 days).
	TTLHours int `json:"ttl_hours,omitempty"`

	// Version is an opaque policy/version token included in the
	// cache key so that research-version changes invalidate
	// previously cached source text.
	Version string `json:"version,omitempty"`
}

// ResearchPolicy bounds external navigation performed by SourceResearch.
type ResearchPolicy struct {
	MaxQueries       int  `json:"max_queries,omitempty"`
	ResultsPerQuery  int  `json:"results_per_query,omitempty"`
	MaxPages         int  `json:"max_pages,omitempty"`
	MaxRounds        int  `json:"max_rounds,omitempty"`
	MinSources       int  `json:"min_sources,omitempty"`
	TimeoutSeconds   int  `json:"timeout_seconds,omitempty"`
	FreshnessDays    int  `json:"freshness_days,omitempty"`
	RequireCitations bool `json:"require_citations,omitempty"`
}

// SourceSpec declares where script-generation input comes from.
type SourceSpec struct {
	Type SourceType `json:"type"`

	Topic      string `json:"topic,omitempty"`
	SourceText string `json:"source_text,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	ClipIDs      []string `json:"clip_ids,omitempty"`
	IntroClipIDs []string `json:"intro_clip_ids,omitempty"`
	NumClips     int      `json:"num_clips,omitempty"`

	Query              string   `json:"query,omitempty"`
	MaxClips           int      `json:"max_clips,omitempty"`
	MinCoverage        float64  `json:"min_coverage,omitempty"`
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`

	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`
	GroundingPolicy  string `json:"grounding_policy,omitempty"`
	FallbackPolicy   string `json:"fallback_policy,omitempty"`
	ForceRefresh     bool   `json:"force_refresh,omitempty"`

	Search          bool   `json:"search,omitempty"`
	AllowTextOnly   bool   `json:"allow_text_only,omitempty"`
	SourceFilter    string `json:"source_filter,omitempty"`
	MediaTypeFilter string `json:"media_type_filter,omitempty"`

	// CachePolicy controls caching of resolved source text per topic.
	// Empty/Disabled means no caching.
	CachePolicy SourceCachePolicy `json:"cache,omitempty"`
	Research    ResearchPolicy    `json:"research,omitempty"`
}

func (s *SourceSpec) IsText() bool     { return s.Type == SourceText }
func (s *SourceSpec) IsClips() bool    { return s.Type == SourceClips }
func (s *SourceSpec) IsCatalog() bool  { return s.Type == SourceCatalog }
func (s *SourceSpec) IsSearch() bool   { return s.Type == SourceSearch }
func (s *SourceSpec) IsCurate() bool   { return s.Type == SourceCurate }
func (s *SourceSpec) IsResearch() bool { return s.Type == SourceResearch }
func (s *SourceSpec) HasClipIDs() bool { return len(s.ClipIDs) > 0 }

// NarrativeClipView is the slot-aware model-facing projection.
type NarrativeClipView struct {
	SlotRef       string `json:"slot_ref"`
	Description   string `json:"description,omitempty"`
	VisualSummary string `json:"visual_summary,omitempty"`
	Transcript    string `json:"transcript,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
}

func (v *NarrativeClipView) Validate() error {
	if v == nil {
		return fmt.Errorf("narrative clip view: nil")
	}
	if strings.TrimSpace(v.SlotRef) == "" {
		return fmt.Errorf("narrative clip view: slot_ref is required")
	}
	return nil
}

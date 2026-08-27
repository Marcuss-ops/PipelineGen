// Package media defines the canonical media-plan contract shared
// between script generation requests and the MediaMemory visual
// resolver. It lives in the domain layer so both the script domain
// and the media-memory domain can depend on it without cycles.
//
// No durable field uses any, any, or map[string]any.
package media

import (
	"encoding/json"
	"fmt"
)

const (
	// MediaPlanModeDisabled skips media resolution entirely.
	MediaPlanModeDisabled string = "disabled"
	// MediaPlanModeManual requires all slots to be covered by locked
	// assignments; no automatic search is performed.
	MediaPlanModeManual string = "manual"
	// MediaPlanModeCacheOnly only queries caches; no external provider
	// is contacted.
	MediaPlanModeCacheOnly string = "cache_only"
	// MediaPlanModeAuto resolves every unset slot automatically using
	// the configured providers.
	MediaPlanModeAuto string = "auto"
	// MediaPlanModeHybrid first honors locked assignments, then cache,
	// then Drive, and finally external providers only when required.
	MediaPlanModeHybrid string = "hybrid"
)

// IsValidMediaPlanMode returns true for supported mode values.
func IsValidMediaPlanMode(mode string) bool {
	switch mode {
	case "", MediaPlanModeDisabled, MediaPlanModeManual, MediaPlanModeCacheOnly,
		MediaPlanModeAuto, MediaPlanModeHybrid:
		return true
	}
	return false
}

// IsActiveMediaPlanMode returns true when mode is a valid, explicitly
// selected media plan mode that is not disabled. Empty mode means that the
// caller did not request visual planning.
func IsActiveMediaPlanMode(mode string) bool {
	return mode != "" && mode != MediaPlanModeDisabled && IsValidMediaPlanMode(mode)
}

// MediaPlanSpec declares how visual media is resolved for each
// segment of a generation item. It is intentionally independent from
// SourceSpec: SourceSpec describes where narrative content comes
// from, while MediaPlanSpec describes which media should accompany
// the generated script.
type MediaPlanSpec struct {
	// Mode is the resolution strategy. Empty means visual planning is off.
	Mode string `json:"mode,omitempty"`

	// ProviderPolicy toggles the canonical visual providers that the
	// VidRush pipeline may consult. Empty/zero values mean "caller did
	// not opt in" and the processor must fail closed on unavailable
	// providers rather than silently assuming support.
	ProviderPolicy MediaProviderPolicy `json:"provider_policy,omitempty"`

	// Assignments are caller-supplied locked media assignments for
	// specific segment/slot combinations. Locked assignments always win.
	Assignments []SegmentMediaAssignment `json:"assignments,omitempty"`

	// Intro and PostSegments describe independent timeline clips. They are
	// resolved by the same closed-candidate resolver as normal media slots.
	Intro        *VisualSlotPlan         `json:"intro,omitempty"`
	PostSegments []PostSegmentVisualPlan `json:"post_segments,omitempty"`
	Variation    *VisualVariationPolicy  `json:"variation_policy,omitempty"`

	// Sources are bounded, caller-supplied source candidates associated
	// with a specific segment and slot. They are not assignments: the
	// resolver must still select/materialize the winning asset.
	Sources []SegmentMediaSource `json:"sources,omitempty"`

	// Searches override the default per-segment/slot search behavior.
	Searches []SegmentMediaSearch `json:"searches,omitempty"`

	// Cache controls read/write behavior for media resolution.
	Cache MediaCachePolicy `json:"cache,omitempty"`

	// Extraction controls the semantic extraction stage that feeds
	// query generation and asset search.
	Extraction MediaExtractionPolicy `json:"extraction,omitempty"`

	// ForceRefresh toggles allow a caller to re-run extraction,
	// asset search, or binding independently without invalidating
	// the whole generation job.
	ForceRefreshExtraction bool `json:"force_refresh_extraction,omitempty"`
	ForceRefreshAssets     bool `json:"force_refresh_assets,omitempty"`
	ForceRefreshBindings   bool `json:"force_refresh_bindings,omitempty"`

	// Planner controls the ranking/planner strategy.
	Planner         MediaPlannerPolicy         `json:"planner,omitempty"`
	Materialization MediaMaterializationPolicy `json:"materialization,omitempty"`
	IncludeTrace    bool                       `json:"include_trace,omitempty"`
}

// UnmarshalJSON accepts the retired providers wire shape only at the JSON
// boundary, then folds it into the single canonical ProviderPolicy field.
func (m *MediaPlanSpec) UnmarshalJSON(data []byte) error {
	type mediaPlanAlias MediaPlanSpec
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	legacyProviders := fields["providers"]
	delete(fields, "providers")
	canonical, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	*m = MediaPlanSpec{}
	if err := json.Unmarshal(canonical, (*mediaPlanAlias)(m)); err != nil {
		return err
	}
	if len(legacyProviders) == 0 || len(m.ProviderPolicy.Artlist) > 0 || len(m.ProviderPolicy.InternetImages) > 0 {
		return nil
	}
	var policy MediaProviderPolicy
	if json.Unmarshal(legacyProviders, &policy) == nil {
		m.ProviderPolicy = policy
		return nil
	}
	var names []string
	if json.Unmarshal(legacyProviders, &names) != nil {
		return fmt.Errorf("invalid legacy media providers")
	}
	for _, name := range names {
		switch name {
		case "artlist":
			m.ProviderPolicy.Artlist = MediaToggleEnabled
		case "internet_images", "images":
			m.ProviderPolicy.InternetImages = MediaToggleEnabled
		}
	}
	return nil
}

const (
	MaterializationMetadataOnly = "metadata_only"
	MaterializationSelected     = "selected"
)

type MediaMaterializationPolicy struct {
	Mode          string `json:"mode,omitempty"`
	UploadToDrive bool   `json:"upload_to_drive,omitempty"`
	EnrichVLM     bool   `json:"enrich_vlm,omitempty"`
	WaitForReady  bool   `json:"wait_for_ready,omitempty"`
}

// MediaExtractionPolicy controls per-segment semantic extraction.
type MediaExtractionPolicy struct {
	Enabled bool `json:"enabled,omitempty"`
	// Device selects local semantic extraction hardware: auto, cpu, or gpu.
	// Auto falls back to CPU only when the optional GPU backend is unavailable.
	Device                        string            `json:"device,omitempty"`
	MaxEntitiesPerSegment         int               `json:"max_entities_per_segment,omitempty"`
	MaxImportantPhrasesPerSegment int               `json:"max_important_phrases_per_segment,omitempty"`
	MaxImportantWordsPerSegment   int               `json:"max_important_words_per_segment,omitempty"`
	MaxArtlistQueriesPerSegment   int               `json:"max_artlist_queries_per_segment,omitempty"`
	MaxImageQueriesPerSegment     int               `json:"max_image_queries_per_segment,omitempty"`
	Strategy                      string            `json:"strategy,omitempty"`
	EntityImages                  EntityImagePolicy `json:"entity_images,omitempty"`
}

type EntityImagePolicy struct {
	Enabled       bool     `json:"enabled,omitempty"`
	EntityTypes   []string `json:"entity_types,omitempty"`
	MaxPerEntity  int      `json:"max_per_entity,omitempty"`
	UploadToDrive bool     `json:"upload_to_drive,omitempty"`
}

// MediaToggle is the local tri-state wire type used by MediaPlanSpec
// so this package does not need to import the script domain package.
type MediaToggle string

const (
	MediaToggleDefault  MediaToggle = "default"
	MediaToggleEnabled  MediaToggle = "enabled"
	MediaToggleDisabled MediaToggle = "disabled"
)

func (t *MediaToggle) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = MediaToggleDefault
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		switch MediaToggle(asString) {
		case MediaToggleDefault, MediaToggleEnabled, MediaToggleDisabled:
			*t = MediaToggle(asString)
			return nil
		}
		return fmt.Errorf("invalid media toggle %q", asString)
	}
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		if asBool {
			*t = MediaToggleEnabled
		} else {
			*t = MediaToggleDisabled
		}
		return nil
	}
	return fmt.Errorf("invalid media toggle value")
}

func (t MediaToggle) MarshalJSON() ([]byte, error) {
	switch t {
	case MediaToggleDefault, MediaToggleEnabled, MediaToggleDisabled:
		return json.Marshal(string(t))
	default:
		return nil, fmt.Errorf("invalid media toggle %q", t)
	}
}

// AsBool reports whether the toggle is enabled. Default and disabled
// both map to false so omitted inputs fail closed.
func (t MediaToggle) AsBool() bool {
	return t == MediaToggleEnabled
}

// MediaProviderPolicy controls which visual providers may be used by
// the VidRush pipeline.
type MediaProviderPolicy struct {
	Artlist         MediaToggle `json:"artlist,omitempty"`
	YouTube         MediaToggle `json:"youtube,omitempty"`
	InternetImages  MediaToggle `json:"internet_images,omitempty"`
	ImageGeneration MediaToggle `json:"image_generation,omitempty"`
}

// Clone returns a deep copy of MediaPlanSpec. Slice fields are
// copied so mutations to the returned instance do not affect the
// original.
func (m MediaPlanSpec) Clone() MediaPlanSpec {
	m.Assignments = append([]SegmentMediaAssignment(nil), m.Assignments...)
	m.Sources = append([]SegmentMediaSource(nil), m.Sources...)
	m.Searches = append([]SegmentMediaSearch(nil), m.Searches...)
	m.PostSegments = append([]PostSegmentVisualPlan(nil), m.PostSegments...)
	if m.Intro != nil {
		intro := m.Intro.Clone()
		m.Intro = &intro
	}
	if m.Variation != nil {
		variation := *m.Variation
		m.Variation = &variation
	}
	return m
}

// SegmentMediaSourceMode controls whether a suggested source is optional
// or must successfully resolve before the segment can be completed.
type SegmentMediaSourceMode string

const (
	SegmentMediaSourceModeSuggested SegmentMediaSourceMode = "suggested"
	SegmentMediaSourceModeRequired  SegmentMediaSourceMode = "required"
)

// IsValidSegmentMediaSourceMode reports whether mode is supported. Empty
// remains valid for backwards compatibility and is treated as suggested.
func IsValidSegmentMediaSourceMode(mode SegmentMediaSourceMode) bool {
	return mode == "" || mode == SegmentMediaSourceModeSuggested || mode == SegmentMediaSourceModeRequired
}

// SegmentMediaSource is a caller-supplied source candidate associated with
// one segment and slot. It deliberately does not represent a selected asset.
type SegmentMediaSource struct {
	SegmentID string                   `json:"segment_id"`
	Slot      SlotKind                 `json:"slot"`
	Provider  string                   `json:"provider"`
	SourceURL string                   `json:"source_url"`
	Query     string                   `json:"query,omitempty"`
	Priority  int                      `json:"priority,omitempty"`
	Mode      SegmentMediaSourceMode   `json:"mode,omitempty"`
}

// SegmentMediaAssignment binds a media asset to a specific segment
// and slot. When Locked is true the resolver must not override it.
type SegmentMediaAssignment struct {
	SegmentID string   `json:"segment_id"`
	Slot      SlotKind `json:"slot"`
	Locked    bool     `json:"locked,omitempty"`
	Asset     MediaRef `json:"asset"`
}

// MediaRef is a discriminated reference to a media asset. Exactly one
// of the asset identifiers should be populated, depending on Kind.
type MediaRef struct {
	// Kind is the asset kind: "clip", "stock", "image", "video".
	Kind string `json:"kind"`

	// AssetID is a canonical platform asset ID (e.g. a Drive-backed
	// stock asset already indexed in the catalog).
	AssetID string `json:"asset_id,omitempty"`

	// ClipID is the ID of a caller-supplied clip.
	ClipID string `json:"clip_id,omitempty"`

	// Provider is the provider that owns this asset (drive, artlist,
	// youtube, pexels, ...).
	Provider string `json:"provider,omitempty"`

	// ProviderAssetID is the provider-side asset identifier.
	ProviderAssetID string `json:"provider_asset_id,omitempty"`

	// SourceURL is a direct URL to the asset. Used only for assets
	// that are not yet materialized in the catalog.
	SourceURL string `json:"source_url,omitempty"`

	// StartMs and EndMs define a temporal window inside a clip or
	// video asset, in milliseconds.
	StartMs int64 `json:"start_ms,omitempty"`
	EndMs   int64 `json:"end_ms,omitempty"`
}

// SegmentMediaSearch overrides the default search behavior for a
// single segment/slot pair. When the same segment/slot appears in
// Assignments with Locked=true, the search is skipped.
type SegmentMediaSearch struct {
	SegmentID  string   `json:"segment_id"`
	Slot       SlotKind `json:"slot"`
	Query      string   `json:"query,omitempty"`
	Providers  []string `json:"providers,omitempty"`
	MediaTypes []string `json:"media_types,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	MinScore   float64  `json:"min_score,omitempty"`
}

// MediaCachePolicy controls cache read/write for media resolution.
type MediaCachePolicy struct {
	Read           bool `json:"read,omitempty"`
	Write          bool `json:"write,omitempty"`
	RefreshIfStale bool `json:"refresh_if_stale,omitempty"`
}

// MediaPlannerPolicy selects the ranking strategy used to choose
// among candidate assets for a slot.
type MediaPlannerPolicy struct {
	Strategy       string `json:"strategy,omitempty"`
	Model          string `json:"model,omitempty"`
	Version        string `json:"version,omitempty"`
	CandidateLimit int    `json:"candidate_limit,omitempty"`
}

// IsValidMediaPlanSlot returns true when slot is a known slot.
func IsValidMediaPlanSlot(slot string) bool {
	return SlotKind(slot).IsValid()
}

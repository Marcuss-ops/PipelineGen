// Package media defines the canonical media-plan contract shared
// between script generation requests and the MediaMemory visual
// resolver. It lives in the domain layer so both the script domain
// and the media-memory domain can depend on it without cycles.
//
// No durable field uses any, any, or map[string]any.
package media

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

// IsActiveMediaPlanMode returns true when mode is a non-empty, valid media
// plan mode that is not disabled. Active modes trigger visual planning.
func IsActiveMediaPlanMode(mode string) bool {
	return mode != "" && mode != MediaPlanModeDisabled && IsValidMediaPlanMode(mode)
}

// MediaPlanSpec declares how visual media is resolved for each
// segment of a generation item. It is intentionally independent from
// SourceSpec: SourceSpec describes where narrative content comes
// from, while MediaPlanSpec describes which media should accompany
// the generated script.
type MediaPlanSpec struct {
	// Mode is the resolution strategy. Empty defaults to "hybrid".
	Mode string `json:"mode,omitempty"`

	// Providers is the ordered list of providers to consider. Empty
	// uses the default set (drive, artlist, pexels, youtube).
	Providers []string `json:"providers,omitempty"`

	// Assignments are caller-supplied locked media assignments for
	// specific segment/slot combinations. Locked assignments always win.
	Assignments []SegmentMediaAssignment `json:"assignments,omitempty"`

	// Searches override the default per-segment/slot search behavior.
	Searches []SegmentMediaSearch `json:"searches,omitempty"`

	// Cache controls read/write behavior for media resolution.
	Cache MediaCachePolicy `json:"cache,omitempty"`

	// Planner controls the ranking/planner strategy.
	Planner         MediaPlannerPolicy         `json:"planner,omitempty"`
	Materialization MediaMaterializationPolicy `json:"materialization,omitempty"`
	IncludeTrace    bool                       `json:"include_trace,omitempty"`
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

// Clone returns a deep copy of MediaPlanSpec. Slice fields are
// copied so mutations to the returned instance do not affect the
// original.
func (m MediaPlanSpec) Clone() MediaPlanSpec {
	m.Providers = append([]string(nil), m.Providers...)
	m.Assignments = append([]SegmentMediaAssignment(nil), m.Assignments...)
	m.Searches = append([]SegmentMediaSearch(nil), m.Searches...)
	return m
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

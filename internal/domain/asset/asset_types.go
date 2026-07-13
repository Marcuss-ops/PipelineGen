package asset

import (
	"encoding/json"
	"time"
)

// Source identifies where an asset originated.
type Source string

// MediaType classifies the content type of an asset. The canonical type
// declaration and const set live in media_type.go; this comment block is
// left here only as a forward pointer for readers scanning asset_types.go
// for the Asset struct. See media_type.go for the full history and
// rationale (Phase 1 local decl → Phase 3 alias of media.MediaType →
// Wave-14 native decl after internal/domain/media is deleted).

// Metadata is an open-ended key-value store for asset properties
// that don't have dedicated columns.
type Metadata map[string]any

// Asset is the canonical domain model for a media asset in PipelineGen.
//
// Extended properties (drive IDs, paths, quality scores, embeddings, etc.)
// are stored in the Metadata map and accessed via typed getter/setter
// methods. This keeps the core struct stable while allowing schema evolution.
type Asset struct {
	ID             string         `json:"id"`
	Source         Source         `json:"source"`
	Name           string         `json:"name"`
	Filename       string         `json:"filename"`
	MediaType      MediaType      `json:"media_type"`
	Category       string         `json:"category"`
	Group          string         `json:"group"`
	SourceURL      string         `json:"source_url"`
	ClipPageURL    string         `json:"clip_page_url"`
	ThumbnailURL   string         `json:"thumbnail_url"`
	Duration       time.Duration  `json:"duration"`
	Tags           []string       `json:"tags"`
	SearchTerms    []string       `json:"search_terms"`
	SearchText     string         `json:"search_text"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	Metadata       Metadata       `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      *time.Time     `json:"deleted_at,omitempty"`

	// ── Step 10 rights surface (PR-CLIPINGEST-PIPELINE, July 2026) ─────
	// Six TYPED extensions added by migration 158 alongside the
	// existing rights_status column (which lives in the Metadata
	// map under the "rights_status" key for historical reasons —
	// see accessors in asset_accessors.go). The 6 NEW fields below
	// are typed first-class struct properties so they show up in
	// Asset JSON serialization without an explicit Metadata-touch
	// round-trip.
	//
	// godlike/06 SSOT: the canonical enums for RightsStatus (6
	// values) + ReviewStatus (4 values) live in rights_state.go.
	// The archcheck forward-prevention gates
	// percheck_rights_status_canonical_6 +
	// percheck_review_status_canonical_4 enforce the count + the
	// shadow-declaration ban.
	//
	// godlike/07 fail-closed: a missing rights_status on a legacy
	// row does NOT panic; the canonical surface
	// (IsPublishable) returns false on the zero value so the
	// SlotSearchPort filter applies uniformly. See
	// internal/application/scripts/ports/clip_search_port.go for
	// the IncludeRightRestricted override flag.
	//
	// LicenseBasis bridges to AssetLicense.id (asset/license_release.go)
	// via a freeform pointer the operator workflow populates; the
	// planner does not dereference the asset_licenses table on
	// hot path (operator-driven workflow, not runtime-resolved).
	LicenseBasis    string       `json:"license_basis"`
	OwnerChannelID  string       `json:"owner_channel_id"`
	AllowedChannels []string     `json:"allowed_channels,omitempty"`
	AllowedRegions  []string     `json:"allowed_regions,omitempty"`
	ExpiresAt       string       `json:"expires_at,omitempty"`
	ReviewStatus    ReviewStatus `json:"review_status,omitempty"`
}

// ── Metadata helpers ────────────────────────────────────────────────

// MetadataJSON returns the Metadata map serialized as a JSON string.
// Returns "{}" if Metadata is nil or empty.
func (m *Asset) MetadataJSON() string {
	if m.Metadata == nil {
		return "{}"
	}
	b, _ := json.Marshal(m.Metadata)
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// SetMetadataJSON parses a JSON string into the Metadata map.
func (m *Asset) SetMetadataJSON(jsonStr string) {
	if jsonStr == "" || jsonStr == "{}" || jsonStr == "null" {
		m.Metadata = make(map[string]any)
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		m.Metadata = make(map[string]any)
		return
	}
	m.Metadata = meta
}

// GetMetadataString retrieves a string value from the Metadata map.
func (m *Asset) GetMetadataString(key string) string {
	if m.Metadata == nil {
		return ""
	}
	v, ok := m.Metadata[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// SetMetadataString sets a string value in the Metadata map.
func (m *Asset) SetMetadataString(key, value string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}

// GetMetadataInt retrieves an int from the Metadata map.
func (m *Asset) GetMetadataInt(key string) int {
	if m.Metadata == nil {
		return 0
	}
	v, ok := m.Metadata[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	default:
		return 0
	}
}

// SetMetadataInt sets an int value in the Metadata map.
func (m *Asset) SetMetadataInt(key string, value int) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}

// Typed accessors (domain-level properties stored in Metadata) live in
// asset_accessors.go per AGENTS.md Pattern 5 godlike/06 SSOT
// one-canonical-owner-per-fact — see that file for all Get/Set methods.

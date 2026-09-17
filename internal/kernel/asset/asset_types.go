package asset

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// Source identifies where an asset originated.
type Source string

// MediaType classifies the content type of an asset. The canonical type
// declaration and const set live in media_type.go; this comment block is
// left here only as a forward pointer for readers scanning asset_types.go
// for the Asset struct. See media_type.go for the full history and
// rationale (Phase 1 local decl → Phase 3 alias of media.MediaType →
// Wave-14 native decl after internal/kernel/media is deleted).

// Metadata is an open-ended key-value store for asset properties
// that don't have dedicated columns.
type Metadata map[string]any

// ImageMetadataMap is a transitional typed alias for image metadata
// while the typed metadata-registry migration is in progress.
// Grandfathered as the canonical map backing
// CanonicalImageMetadataBuilder (see percheck_metadataregistry.go).
type ImageMetadataMap = map[string]any

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
	ProviderTags   []string       `json:"provider_tags,omitempty"`
	VLMTags        []string       `json:"vlm_tags,omitempty"`
	ManualTags     []string       `json:"manual_tags,omitempty"`
	TranscriptTags []string       `json:"transcript_tags,omitempty"`
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
	// internal/capabilities/scripts/ports/clip_search_port.go for
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

// RebuildTags rebuilds the aggregated Tags field from the source-specific
// tag slices (ProviderTags, VLMTags, ManualTags, TranscriptTags) while
// preserving the original order and removing duplicates.
func (a *Asset) RebuildTags() {
	capacity := len(a.ProviderTags) + len(a.VLMTags) + len(a.ManualTags) + len(a.TranscriptTags)
	combined := make([]string, 0, capacity)
	combined = append(combined, a.ProviderTags...)
	combined = append(combined, a.VLMTags...)
	combined = append(combined, a.ManualTags...)
	combined = append(combined, a.TranscriptTags...)
	a.Tags = sliceutil.UniqueStrings(combined)
}

// SyncTagFieldsToMetadata persists the source-specific tag slices into
// the Metadata map so they survive storage in the metadata_json column.
func (a *Asset) SyncTagFieldsToMetadata() {
	if a.Metadata == nil {
		a.Metadata = make(map[string]any)
	}
	a.Metadata["provider_tags"] = a.ProviderTags
	a.Metadata["vlm_tags"] = a.VLMTags
	a.Metadata["manual_tags"] = a.ManualTags
	a.Metadata["transcript_tags"] = a.TranscriptTags
}

// SyncTagFieldsFromMetadata restores the source-specific tag slices from
// the Metadata map after a load from the database.
func (a *Asset) SyncTagFieldsFromMetadata() {
	if a.Metadata == nil {
		return
	}
	a.ProviderTags = getStringSlice(a.Metadata, "provider_tags")
	a.VLMTags = getStringSlice(a.Metadata, "vlm_tags")
	a.ManualTags = getStringSlice(a.Metadata, "manual_tags")
	a.TranscriptTags = getStringSlice(a.Metadata, "transcript_tags")
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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

// ─── Ref: the canonical, location-free identity of a media asset ────────
//
// # The one distinction this section exists to enforce
//
//	AssetID  = WHAT this asset is logically
//	SHA256   = WHICH BYTES it is
//
// They are independent facts and are allowed to change independently: replacing
// the portrait behind "entity-image-person-michael-jordan-primary" keeps the
// asset id and changes the content address, while two different logical assets
// may legitimately share one address (in the content-addressed store there is
// then exactly one physical copy).
//
// # Why there is no location field here, and never will be
//
// A record that carries a content address AND a location has two sources of
// truth for one fact. Every individual consumer looks correct, so the
// disagreement only becomes visible much later, in a different process, as
// "the hash does not match the bytes" or "the file is not there". That is the
// production failure the media identity gate
// (cmd/archcheck/scan/governance/percheck_media_identity_no_location_fields.go)
// exists to prevent, and this type is the shape it wants every identity to
// converge on:
//
//	identity   → this type      (asset_id + content address)
//	location   → asset_locations in the PostgreSQL media SSOT (provider, file id, links)
//	local path → a runtime cache, valid for seconds in ONE process, owned by the
//	             (future) canonical AssetMaterializer — never a domain field
//
// Because the type carries no location, it is safe to serialize, log, persist,
// hash and compare: two Refs with the same Canonical() value describe the same
// bytes, wherever those bytes currently live.
//
// NOTE ON PLACEMENT: this type was authored in its own file (`ref.go`). It lives
// here instead because internal/kernel/asset is a REGISTERED hotspot with a
// frozen production-file count (architecture/package_hotspots.json, baseline 39,
// decomposition deadline 2026-09-30) and the carry-forward ratchet
// (percheck_legacy_hotspot_growth) is a hard gate that fails on any new file in
// a registered hotspot. Merging the section keeps `asset.Ref` at its canonical
// import path with no call-site churn and no increase in registered debt.

// Sentinel failures of Validate. They are distinct so a caller can tell a
// logical identity gap ("we do not know which asset this is") from a content
// gap ("we do not know which bytes these are") — the two need different
// remediation.
var (
	// ErrRefAssetIDRequired reports a Ref with no logical identity.
	ErrRefAssetIDRequired = errors.New("asset ref: asset_id is required")
	// ErrRefContentHashRequired reports a Ref with no content address.
	ErrRefContentHashRequired = errors.New("asset ref: sha256 content address is required")
)

// Ref is the canonical identity of a media asset: what it is (AssetID) and
// which bytes it is (SHA256). MediaType and SizeBytes describe those bytes.
//
// Every field is safe to cross a wire, a log line or a database row. No field
// is a location.
type Ref struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// New builds a Ref, trimming the logical fields and canonicalising the digest.
func New(assetID, sha256, mediaType string, sizeBytes int64) Ref {
	return Ref{AssetID: assetID, SHA256: sha256, MediaType: mediaType, SizeBytes: sizeBytes}.Canonical()
}

// IsZero reports whether the Ref carries no identity at all.
func (r Ref) IsZero() bool {
	return strings.TrimSpace(r.AssetID) == "" && strings.TrimSpace(r.SHA256) == "" && r.MediaType == "" && r.SizeBytes == 0
}

// HasContentAddress reports whether the Ref names concrete bytes.
func (r Ref) HasContentAddress() bool {
	return strings.TrimSpace(r.SHA256) != ""
}

// Validate reports whether the Ref is a usable identity: a logical asset AND
// the bytes it stands for. A Ref missing either half cannot be dereferenced,
// so a boundary that persists or publishes one must refuse it rather than
// write a half-identity that later readers cannot resolve.
func (r Ref) Validate() error {
	if strings.TrimSpace(r.AssetID) == "" {
		return ErrRefAssetIDRequired
	}
	if strings.TrimSpace(r.SHA256) == "" {
		return ErrRefContentHashRequired
	}
	return nil
}

// Canonical returns the Ref with its logical fields trimmed and its content
// address lower-cased.
//
// The digest spelling matters because the content address is a COMPARISON KEY:
// the pipeline deduplicates assets by it, and two spellings of one digest
// ("ABC…" and "abc…") would otherwise be treated as two different assets — the
// same one-digest-two-addresses problem the object store refuses on disk. The
// canonical spelling is therefore applied at the identity boundary, once,
// instead of by every consumer that happens to compare hashes (several did, as
// `strings.ToLower(ref.SHA256)` scattered across the enqueue paths).
func (r Ref) Canonical() Ref {
	r.AssetID = strings.TrimSpace(r.AssetID)
	r.SHA256 = strings.ToLower(strings.TrimSpace(r.SHA256))
	r.MediaType = strings.TrimSpace(r.MediaType)
	return r
}

// ContentHash returns the canonical content address. It is an accessor rather
// than a second field: there is exactly one content address in this type, and
// this name exists so callers migrating from the older `ContentHash()` spelling
// (kernel/asset.Asset) do not need to re-derive it.
func (r Ref) ContentHash() string {
	return r.Canonical().SHA256
}

// Equal reports whether two Refs describe the same asset and the same bytes.
// It compares canonicalised values, so a differently-cased digest is the same
// asset rather than a mysterious second one.
func (r Ref) Equal(other Ref) bool {
	return r.Canonical() == other.Canonical()
}

// DedupKey is the key that makes a set of Refs a set of ASSETS rather than a set
// of references: the content address when there is one, else the logical id.
//
// Deduplicating by logical id alone would copy one payload once per reference;
// deduplicating by address alone would collapse two logical assets that share
// bytes — which is correct for the STORE and wrong for the identity. Callers
// that need the store's view use Canonical().SHA256 directly.
func (r Ref) DedupKey() string {
	if canonical := r.Canonical(); canonical.SHA256 != "" {
		return canonical.SHA256
	}
	return strings.TrimSpace(r.AssetID)
}

// IsCanonicalDigest reports whether SHA256 is a full 64-hex-character digest.
//
// It is deliberately NOT part of Validate: fixtures, golden plans and
// compatibility records legitimately carry short synthetic markers, and
// rejecting those would refuse plans the pipeline is supposed to render. This
// method exists so a caller that needs the STRONG claim (an object-store
// address, a recorded certification) can ask for it explicitly and fail closed
// on its own terms.
func (r Ref) IsCanonicalDigest() bool {
	digest := r.Canonical().SHA256
	if len(digest) != 64 {
		return false
	}
	for i := 0; i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// String renders the identity for logs and errors. It never includes a path,
// because the type cannot hold one.
func (r Ref) String() string {
	canonical := r.Canonical()
	digest := canonical.SHA256
	if len(digest) > 12 {
		digest = digest[:12]
	}
	if digest == "" {
		digest = "-"
	}
	if canonical.MediaType == "" {
		return fmt.Sprintf("%s@%s", canonical.AssetID, digest)
	}
	return fmt.Sprintf("%s@%s (%s)", canonical.AssetID, digest, canonical.MediaType)
}

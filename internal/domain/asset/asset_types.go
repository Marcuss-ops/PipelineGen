package asset

import (
	"encoding/json"
	"strings"
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

// LifecycleState tracks where an asset is in its lifecycle.
//
// TODO 3 close-out (June 2026): the lifecycle vocabulary is now SSOT.
// New writes (Qdrant payload, media_assets columns, ingest paths)
// MUST use only the canonical uppercase constants below. The legacy
// lowercase `StateReady`/`StatePending` constants are retained as
// read-only aliases so existing in-memory references in the codebase
// continue to compile, but they are NOT valid states (`Valid()` returns
// false for them) and the SSOT canonicalisers `canonicalLifecycleState`
// / `NormalizeLegacyLifecycle` map them to canonical uppercase form
// on read. After followup ingest-path migration (TODO 3 followup),
// the legacy constants will be removed.
//
// Vocabulary (canonical, uppercase, SSOT):
//
//	StateStaging         "STAGING"        — created, not yet indexed
//	StateProcessing      "PROCESSING"     — ingestion in progress
//	StateActive          "ACTIVE"         — live and queryable
//	LcStateDeletePending   "DELETE_PENDING" — delete acknowledged, reconciler
//	                                       cleanup pending
//	StateDeleted         "DELETED"        — terminal, excluded from search
//	StateError           "ERROR"          — index failed; not queryable
type LifecycleState string

const (
	// Canonical vocabulary — always uppercase, always valid.
	StateStaging       LifecycleState = "STAGING"
	StateProcessing    LifecycleState = "PROCESSING"
	StateActive        LifecycleState = "ACTIVE"
	LcStateDeletePending LifecycleState = "DELETE_PENDING"
	StateDeleted       LifecycleState = "DELETED"
	StateError         LifecycleState = "ERROR"

	// Legacy bits retained for compile compatibility ONLY. Do NOT
	// introduce new writes using these — they are mapped to canonical
	// forms by NormalizeLegacyLifecycle / canonicalLifecycleState.
	// Canonical writes use StateActive / StateStaging.
	// Remove in a followup once all ingest paths migrate.
	StateReady   LifecycleState = "ready"
	StatePending LifecycleState = "pending"
)

// Valid returns true if s is a CANONICAL lifecycle state. Legacy
// lowercase values (StateReady, StatePending) and any unknown value
// return false — canonicalLifecycleState is the only legal path for
// materialising a lifecycle_state payload value.
//
// TODO 3 close-out (June 2026): post-migration, Valid becomes the
// single gate for SSOT compliance. Anything that reaches a write
// without passing through canonicalLifecycleState fails this gate.
func (s LifecycleState) Valid() bool {
	switch s {
	case StateStaging, StateProcessing, StateActive,
		LcStateDeletePending, StateDeleted, StateError:
		return true
	}
	return false
}

// NormalizeLegacyLifecycle maps any raw status value (uppercase,
// lowercase, or mixed-case) to the canonical uppercase LifecycleState.
// Exposed for callers reading legacy data where lowercase values
// like "ready"/"pending" may persist in SQLite status columns,
// incoming payload fields from older Qdrant points, or external
// importers.
//
// Mapping contract:
//
//	"", unknown                       → StateActive (canonical default)
//	"ACTIVE", "active", "ready",     → StateActive
//	"SEARCHABLE", "searchable"       → StateActive (legacy alias)
//	"STAGING", "staging", "pending"  → StateStaging
//	"PENDING", "pending"             → StateStaging (legacy alias)
//	"PROCESSING", "processing"       → StateProcessing
//	"DELETE_PENDING", "delete_pending" → LcStateDeletePending
//	"DELETED", "deleted"             → StateDeleted
//	"ERROR", "error"                 → StateError
//
// Whitespace is trimmed and case is folded upper before lookup, so
// "  Ready ", "READY", "ready" all collapse to StateActive.
func NormalizeLegacyLifecycle(raw string) LifecycleState {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "STAGING", "PENDING":
		return StateStaging
	case "PROCESSING":
		return StateProcessing
	case "ACTIVE", "READY", "SEARCHABLE":
		return StateActive
	case "DELETE_PENDING":
		return LcStateDeletePending
	case "DELETED":
		return StateDeleted
	case "ERROR":
		return StateError
	case "":
		return StateActive
	default:
		return StateActive
	}
}

// CanonicalLifecycleState resolves a primary (preferred) lifecycle value
// to the canonical uppercase LifecycleState, with optional fallback to
// a legacy status string.
//
// The rule (TODO 3 close-out, June 2026):
//
//  1. primary non-empty → NormalizeLegacyLifecycle(primary).
//     This handles canonical pass-through as well as legacy lowercase
//     values like "ready"/"pending" pre-existing in the codebase.
//  2. primary empty → fallback through NormalizeLegacyLifecycle.
//     Used by callers that distinguish between an explicit lifecycle
//     column and a legacy status column (asset_store.go::FetchAsset).
//  3. Both empty / unknown → StateActive (canonical default).
//
// This is the SSOT canonicaliser for the Qdrant payload `lifecycle_state`
// key, the media_assets.lifecycle_state column, and any in-memory
// LifecycleState value. Callers MUST NOT bypass it — see
// payload_mapper.go::BuildPayload and asset_store.go::FetchAsset in
// internal/infrastructure/qdrant, as well as any domain code that
// produces a canonical value from a raw string.
//
// Exported (uppercase C) because infrastructure callers import it; the
// domain-internal wrapper on *Asset below is kept as a convenience for
// callers that already hold an *Asset reference.
func CanonicalLifecycleState(primary string, fallback string) LifecycleState {
	if primary != "" {
		return NormalizeLegacyLifecycle(primary)
	}
	if fallback != "" {
		return NormalizeLegacyLifecycle(fallback)
	}
	return StateActive
}

// canonicalLifecycleState is the domain-internal wrapper that
// resolves an Asset's LifecycleState to the canonical form. Infrastructure
// callers (qdrant payload_mapper, qdrant asset_store) use the
// exported CanonicalLifecycleState(primary, fallback); this
// pointer-based variant exists for domain code that already holds
// an *Asset reference and wants the SSOT canonicalisation without
// the fallback indirection. nil → StateActive (defensive default).
func canonicalLifecycleState(a *Asset) LifecycleState {
	if a == nil {
		return StateActive
	}
	return CanonicalLifecycleState(string(a.LifecycleState), "")
}

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

// ── Typed accessors (domain-level properties stored in Metadata) ────

func (m *Asset) ExternalURL() string        { return m.SourceURL }
func (m *Asset) SetExternalURL(v string)    { m.SourceURL = v }
func (m *Asset) DriveFileID() string        { return m.GetMetadataString("drive_file_id") }
func (m *Asset) SetDriveFileID(v string)    { m.SetMetadataString("drive_file_id", v) }
func (m *Asset) DriveLink() string          { return m.GetMetadataString("drive_link") }
func (m *Asset) SetDriveLink(v string)      { m.SetMetadataString("drive_link", v) }
func (m *Asset) DownloadLink() string       { return m.GetMetadataString("download_link") }
func (m *Asset) SetDownloadLink(v string)   { m.SetMetadataString("download_link", v) }
func (m *Asset) LocalPath() string          { return m.GetMetadataString("local_path") }
func (m *Asset) SetLocalPath(v string)      { m.SetMetadataString("local_path", v) }
func (m *Asset) FileHash() string           { return m.GetMetadataString("file_hash") }
func (m *Asset) SetFileHash(v string)       { m.SetMetadataString("file_hash", v) }
func (m *Asset) FolderID() string           { return m.GetMetadataString("folder_id") }
func (m *Asset) SetFolderID(v string)       { m.SetMetadataString("folder_id", v) }
func (m *Asset) FolderPath() string         { return m.GetMetadataString("folder_path") }
func (m *Asset) SetFolderPath(v string)     { m.SetMetadataString("folder_path", v) }
func (m *Asset) ParentFolderID() string     { return m.GetMetadataString("parent_folder_id") }
func (m *Asset) SetParentFolderID(v string) { m.SetMetadataString("parent_folder_id", v) }
func (m *Asset) Depth() int                 { return m.GetMetadataInt("depth") }
func (m *Asset) SetDepth(v int)             { m.SetMetadataInt("depth", v) }

func (m *Asset) IsFolder() bool {
	if m.Metadata == nil {
		return false
	}
	v, ok := m.Metadata["is_folder"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (m *Asset) SetIsFolder(v bool) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["is_folder"] = v
}

func (m *Asset) ChildCount() int       { return m.GetMetadataInt("child_count") }
func (m *Asset) SetChildCount(v int)   { m.SetMetadataInt("child_count", v) }
func (m *Asset) SceneType() string     { return m.GetMetadataString("scene_type") }
func (m *Asset) SetSceneType(v string) { m.SetMetadataString("scene_type", v) }

func (m *Asset) QualityScore() float64 {
	if m.Metadata == nil {
		return 0
	}
	v, ok := m.Metadata["quality_score"]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

func (m *Asset) SetQualityScore(v float64) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["quality_score"] = v
}

func (m *Asset) ReuseCount() int        { return m.GetMetadataInt("reuse_count") }
func (m *Asset) SetReuseCount(v int)    { m.SetMetadataInt("reuse_count", v) }
func (m *Asset) LastUsedAt() string     { return m.GetMetadataString("last_used_at") }
func (m *Asset) SetLastUsedAt(v string) { m.SetMetadataString("last_used_at", v) }

func (m *Asset) UsableFor() []string {
	if m.Metadata == nil {
		return nil
	}
	v, ok := m.Metadata["usable_for"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}

func (m *Asset) SetUsableFor(v []string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["usable_for"] = v
}

func (m *Asset) AvoidFor() []string {
	if m.Metadata == nil {
		return nil
	}
	v, ok := m.Metadata["avoid_for"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}

func (m *Asset) SetAvoidFor(v []string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["avoid_for"] = v
}

func (m *Asset) PHash() string                   { return m.GetMetadataString("phash") }
func (m *Asset) SetPHash(v string)               { m.SetMetadataString("phash", v) }
func (m *Asset) EmbeddingJSON() string           { return m.GetMetadataString("embedding_json") }
func (m *Asset) SetEmbeddingJSON(v string)       { m.SetMetadataString("embedding_json", v) }
func (m *Asset) VisualEmbedding() string         { return m.GetMetadataString("visual_embedding") }
func (m *Asset) SetVisualEmbedding(v string)     { m.SetMetadataString("visual_embedding", v) }
func (m *Asset) TranscriptEmbedding() string     { return m.GetMetadataString("transcript_embedding") }
func (m *Asset) SetTranscriptEmbedding(v string) { m.SetMetadataString("transcript_embedding", v) }
func (m *Asset) VisualEmbeddingJSON() string     { return m.GetMetadataString("visual_embedding_json") }
func (m *Asset) SetVisualEmbeddingJSON(v string) { m.SetMetadataString("visual_embedding_json", v) }

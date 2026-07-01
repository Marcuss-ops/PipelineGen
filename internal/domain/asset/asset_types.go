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

// LifecycleState is the SINGLE canonical enum for the asset lifecycle
// (PR 1 — Lifecycle state SSOT, June 2026). Six values, all UPPERCASE.
// The previous lowercase compat values (`ready`, `pending`) and the
// dual-enum AssetStatus (lifecycle_core.go) have been retired.
//
// Migration history:
//   - Legacy compat (pre-PR1): lowercase values mixed with canonical
//     uppercase values in media_assets.lifecycle_state, plus a
//     parallel `status` column with its own lowercase enum (`active`,
//     `archived`, `deleted`, `processing`, `failed`). Reads consulted
//     the COALESCE fallback (NULLIF(lifecycle_state), NULLIF(status)).
//   - PR1: AssetStatus enum deleted; `status` column dropped by
//     migration 101; writers use only these 6 constants; readers
//     read the column directly without fallback.
type LifecycleState string

const (
	// StateStaging — asset row created but not yet indexable.
	StateStaging LifecycleState = "STAGING"
	// StateProcessing — vectorisation currently in flight.
	StateProcessing LifecycleState = "PROCESSING"
	// StateActive — terminal-and-searchable default for indexable rows.
	// This is the canonical payload value at Qdrant and the only state
	// returned by the /internal/v1/media/search endpoint.
	StateActive LifecycleState = "ACTIVE"
	// StateDeletePending — LEGACY broad intent state (pre-Blocco 3.1,
	// June 2026). Reads that previously matched DELETE_PENDING as a
	// single "soft-delete initiated" intent must now distinguish
	// between the three explicit deletion steps; new producers MUST
	// write StateDeleteRequested and follow the chain. Kept here so
	// in-flight rows that predate the state-machine migration stay
	// visible to operators + the reconciler can rewrite them on its
	// next pass.
	StateDeletePending LifecycleState = "DELETE_PENDING"
	// StateDeleteRequested (Blocco 3.1, June 2026) — first hop of
	// the chain. Set by Dispatcher.EnqueueDriveDelete in the same tx
	// that emits the asset.drive.delete_requested.v1 outbox event.
	// The DriveDeleteHandler pre-flight accepts {DELETE_REQUESTED,
	// DRIVE_DELETE_PENDING} so a re-enqueue on the same asset
	// doesn't reset the chain.
	StateDeleteRequested LifecycleState = "DELETE_REQUESTED"
	// StateDriveDeletePending — Drive Trash (or Delete for hard-
	// deletion) is in flight or retrying. DriveDeleteHandler stamps
	// this BEFORE the Drive API call and leaves the row in this
	// state on transient failure. The reconciler picks the row up
	// if the state has been stuck > reconciliationThreshold.
	StateDriveDeletePending LifecycleState = "DRIVE_DELETE_PENDING"
	// StateLifecycleIndexDeletePending — Drive delete succeeded; the
	// Qdrant DeletePoints + media_assets SoftDelete chain is in
	// flight or retrying. IndexDeleteHandler pre-flights on this
	// state and stamps DELETED on success.
	StateLifecycleIndexDeletePending LifecycleState = "INDEX_DELETE_PENDING"
	// StateDeleted — terminal tombstone. The SoftDeleteFilter and
	// the Qdrant lifecycle waterfall exclude this state from all
	// reads.
	StateDeleted LifecycleState = "DELETED"
	// StateError — indexer failed and could not recover; surfaced
	// to operators via dashboards and the reaper diagnostic.
	StateError LifecycleState = "ERROR"
)

// CanonicalLifecycleStateValues returns the closed enumeration of
// canonical lifecycle_state strings. Callers use this as the
// single-source-of-truth list for migrations, dashboards, and
// qdrant payload validation. StateDeletePending is the legacy
// broad-intent value kept for in-flight migration; new writes use
// the 3 explicit deletion states added in Blocco 3.1.
func CanonicalLifecycleStateValues() []LifecycleState {
	return []LifecycleState{
		StateStaging,
		StateProcessing,
		StateActive,
		StateDeletePending,
		StateDeleteRequested,
		StateDriveDeletePending,
		StateLifecycleIndexDeletePending,
		StateDeleted,
		StateError,
	}
}

// Valid returns true if s is a known canonical lifecycle state
// (Blocco 3.1: includes the 3 explicit deletion states).
func (s LifecycleState) Valid() bool {
	switch s {
	case StateStaging, StateProcessing, StateActive,
		StateDeletePending, StateDeleteRequested,
		StateDriveDeletePending, StateLifecycleIndexDeletePending,
		StateDeleted, StateError:
		return true
	}
	return false
}

// IsValidTransition reports whether moving from `from` to `to` is
// one of the allowed edges of the deletion state machine (Blocco 3.1,
// June 2026).
//
// Strict-machine contract:
//
//	ACTIVE              → DELETE_REQUESTED        (user-initiated delete)
//	DELETE_REQUESTED    → DRIVE_DELETE_PENDING    (DriveDeleteHandler pre-flip)
//	DRIVE_DELETE_PENDING→ INDEX_DELETE_PENDING    (DriveDeleteHandler post-success flip)
//	INDEX_DELETE_PENDING→ DELETED                 (IndexDeleteHandler post-success flip)
//	*                   → ACTIVE                  (restore path is symmetric; see
//	                                               Restore handler + IsValidRestoreTransition)
//
// Self-loops are allowed and idempotent (writing the same state
// twice in a row is harmless). StateDeletePending is the LEGACY
// broad-intent value; transitions FROM it are allowed into the new
// chain so the legacy migration path stays valid:
//
//	DELETE_PENDING      → DRIVE_DELETE_PENDING    (legacy rewrite path)
//
// All other transitions (including the terminal DELETED state) are
// rejected. Callers using SetLifecycleStateTx + an explicit
// transition check get a typed error rather than a silent row flip;
// the write itself is gated by IsValidTransition so a programmer
// error becomes a build-time constraint rather than a runtime
// tombstone of an ACTIVE row.
func (s LifecycleState) IsValidTransition(to LifecycleState) bool {
	if s == to {
		return true // idempotent self-loop
	}
	switch s {
	case StateActive:
		return to == StateDeleteRequested || to == StateDeletePending
	case StateDeleteRequested:
		return to == StateDriveDeletePending
	case StateDeletePending:
		// Legacy broad-intent → Drive-pending rewrite path. Drives
		// the reconciler's rewrite of pre-Blocco 3.1 rows.
		return to == StateDriveDeletePending || to == StateDeleteRequested
	case StateDriveDeletePending:
		return to == StateLifecycleIndexDeletePending
	case StateLifecycleIndexDeletePending:
		return to == StateDeleted
	}
	return false
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

package assets

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when an asset, artifact, or delivery is not found.
var ErrNotFound = errors.New("not found")
var ErrAlreadyExists = errors.New("asset already exists")
var ErrInvalidID = errors.New("invalid asset ID")
var ErrSoftDeleted = errors.New("asset is soft-deleted")

// ── Types ───────────────────────────────────────────────────────────

type Source string
type MediaType string
type Metadata map[string]any

// ── LifecycleState ──────────────────────────────────────────────────

type LifecycleState string

const (
	StateStaging    LifecycleState = "STAGING"
	StateProcessing LifecycleState = "PROCESSING"
	StateActive     LifecycleState = "ACTIVE"
	StateDeleted    LifecycleState = "DELETED"

	// Legacy LifecycleState compatibility values
	StateReady   LifecycleState = "ready"
	StatePending LifecycleState = "pending"
)

// Valid returns true if s is a known lifecycle state.
func (s LifecycleState) Valid() bool {
	switch s {
	case StateStaging, StateProcessing, StateActive, StateDeleted, StateReady, StatePending:
		return true
	}
	return false
}

// ── Asset ───────────────────────────────────────────────────────────

// Asset represents the canonical model of a media asset in PipelineGen.
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

// ── Location ────────────────────────────────────────────────────────

// LocationKind categorises where a Location physically lives.
type LocationKind string

const (
	LocationKindLocal         LocationKind = "local"
	LocationKindDrive         LocationKind = "drive"
	LocationKindObjectStorage LocationKind = "object_storage"
)

// Location is the canonical domain entity for an asset_locations row.
type Location struct {
	ID            int64        `json:"id"`
	AssetID       string       `json:"asset_id"`
	LocationKind  LocationKind `json:"location_kind"`
	URI           string       `json:"uri"`
	ExternalID    string       `json:"external_id,omitempty"`
	AccessURL     string       `json:"access_url,omitempty"`
	DownloadURL   string       `json:"download_url,omitempty"`
	MimeType      string       `json:"mime_type"`
	FileSizeBytes int64        `json:"file_size_bytes"`
	FileHash      string       `json:"file_hash"`
	IsPrimary     bool         `json:"is_primary"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// ── Processing ──────────────────────────────────────────────────────

// ProcessingStatus is the 4-state lifecycle of a processing step.
type ProcessingStatus string

const (
	StatusPending   ProcessingStatus = "pending"
	StatusRunning   ProcessingStatus = "running"
	StatusCompleted ProcessingStatus = "completed"
	StatusFailed    ProcessingStatus = "failed"
)

// ProcessingStage is a canonical processing step name.
type ProcessingStage string

const (
	StageDownload      ProcessingStage = "download"
	StageNormalize     ProcessingStage = "normalize"
	StageTranscription ProcessingStage = "transcription"
	StageEmbedding     ProcessingStage = "embedding"
	StageIndexing      ProcessingStage = "indexing"
	StageUpload        ProcessingStage = "upload"
	StageVerify        ProcessingStage = "verify"
	StageCleanup       ProcessingStage = "cleanup"
)

// ProcessingRecord represents a single asset_processing row.
type ProcessingRecord struct {
	AssetID      string           `json:"asset_id"`
	Step         string           `json:"step"`
	Status       ProcessingStatus `json:"status"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	AttemptCount int              `json:"attempt_count"`
	MetadataJSON string           `json:"metadata_json,omitempty"`
}

// ── Version ─────────────────────────────────────────────────────────

// Version represents a single asset_versions row.
type Version struct {
	ID            int64     `json:"id"`
	AssetID       string    `json:"asset_id"`
	VersionNumber int       `json:"version_number"`
	SourceURI     string    `json:"source_uri"`
	FileHash      string    `json:"file_hash"`
	FileSizeBytes int64     `json:"file_size_bytes"`
	MimeType      string    `json:"mime_type"`
	MetadataJSON  string    `json:"metadata_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ── Artifact ────────────────────────────────────────────────────────

type ArtifactStatus string

const (
	ArtifactStaging     ArtifactStatus = "STAGING"
	ArtifactVerifying   ArtifactStatus = "VERIFYING"
	ArtifactReady       ArtifactStatus = "READY"
	ArtifactFailed      ArtifactStatus = "FAILED"
	ArtifactQuarantined ArtifactStatus = "QUARANTINED"
	ArtifactDeleted     ArtifactStatus = "DELETED"
)

type Artifact struct {
	ID             string         `json:"id"`
	JobID          string         `json:"job_id,omitempty"`
	Kind           string         `json:"kind"` // video, audio, thumbnail, image
	Status         ArtifactStatus `json:"status"`
	StorageBackend string         `json:"storage_backend"` // local, s3
	StorageKey     string         `json:"storage_key"`     // canonical blob path
	SHA256         string         `json:"sha256"`
	SizeBytes      int64          `json:"size_bytes"`
	MimeType       string         `json:"mime_type"`
	DurationMs     int            `json:"duration_ms,omitempty"`
	Width          int            `json:"width,omitempty"`
	Height         int            `json:"height,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	VerifiedAt     *time.Time     `json:"verified_at,omitempty"`
	LastAccessedAt *time.Time     `json:"last_accessed_at,omitempty"`
}

// ── Delivery ────────────────────────────────────────────────────────

type DeliveryStatus string

const (
	DeliveryPending     DeliveryStatus = "PENDING"
	DeliveryLeased      DeliveryStatus = "LEASED"
	DeliveryRunning     DeliveryStatus = "RUNNING"
	DeliveryRetryWait   DeliveryStatus = "RETRY_WAIT"
	DeliverySucceeded   DeliveryStatus = "SUCCEEDED"
	DeliveryFailed      DeliveryStatus = "FAILED"
	DeliveryBlockedAuth DeliveryStatus = "BLOCKED_AUTH"
	DeliveryCancelled   DeliveryStatus = "CANCELLED"
)

type Delivery struct {
	ID               string         `json:"id"`
	ArtifactID       string         `json:"artifact_id"`
	DestinationID    string         `json:"destination_id"`
	Provider         string         `json:"provider"` // drive, youtube, s3
	Status           DeliveryStatus `json:"status"`
	AttemptCount     int            `json:"attempt_count"`
	MaxAttempts      int            `json:"max_attempts"`
	NextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
	LockedBy         string         `json:"locked_by,omitempty"`
	LockedUntil      *time.Time     `json:"locked_until,omitempty"`
	RemoteID         string         `json:"remote_id,omitempty"`
	RemoteURL        string         `json:"remote_url,omitempty"`
	LastErrorCode    string         `json:"last_error_code,omitempty"`
	LastErrorMessage string         `json:"last_error_message,omitempty"`
	IdempotencyKey   string         `json:"idempotency_key,omitempty"`
	StorageKey       string         `json:"storage_key,omitempty"`
	SHA256           string         `json:"sha256,omitempty"`
	SizeBytes        int64          `json:"size_bytes,omitempty"`
	MimeType         string         `json:"mime_type,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
}

type DeliveryDestination struct {
	DestinationID string `json:"destination_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Enabled       bool   `json:"enabled"`
	ConfigJSON    string `json:"config_json,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// ── Filter ──────────────────────────────────────────────────────────

type Filter struct {
	Source       string   `json:"source,omitempty"`
	MediaType    string   `json:"media_type,omitempty"`
	States       []string `json:"states,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	ExcludeIDs   []string `json:"exclude_ids,omitempty"`
	HasEmbedding *bool    `json:"has_embedding,omitempty"`
	IsFolder     *bool    `json:"is_folder,omitempty"`
	Category     string   `json:"category,omitempty"`
	Group        string   `json:"group_name,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`
}

// ── Store Interfaces ────────────────────────────────────────────────

// Store represents the unified CRUD repository for assets.
type Store interface {
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Save(ctx context.Context, asset *Asset) error
	Delete(ctx context.Context, id string) error
}

// Repository represents the legacy/intermediate repository interface for assets.
type Repository interface {
	Upsert(ctx context.Context, asset *Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Count(ctx context.Context, filter Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
}

// LocationRepository is the canonical domain contract for asset_locations persistence.
type LocationRepository interface {
	Upsert(ctx context.Context, loc *Location) error
	GetPrimary(ctx context.Context, assetID string) (*Location, error)
	ListByAsset(ctx context.Context, assetID string) ([]*Location, error)
	SetPrimary(ctx context.Context, assetID string, kind LocationKind) error
	Delete(ctx context.Context, assetID string, kind LocationKind) error
	DeleteAll(ctx context.Context, assetID string) error
}

// ProcessingRepository is the canonical domain contract for asset_processing persistence.
type ProcessingRepository interface {
	Start(ctx context.Context, assetID, step string) error
	Complete(ctx context.Context, assetID, step string) error
	Fail(ctx context.Context, assetID, step, errMsg string) error
	Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error
	Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error)
	GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error)
	GetFailed(ctx context.Context) ([]ProcessingRecord, error)
	Delete(ctx context.Context, assetID, step string) error
	DeleteAll(ctx context.Context, assetID string) error
}

// VersionRepository is the canonical domain contract for asset_versions persistence.
type VersionRepository interface {
	GetCurrent(ctx context.Context, assetID string) (*Version, error)
	List(ctx context.Context, assetID string) ([]Version, error)
	Append(ctx context.Context, v *Version) error
}

// ArtifactStore manages metadata for stored artifacts.
type ArtifactStore interface {
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status ArtifactStatus, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]*Artifact, error)
}

// DeliveryStore manages delivery records.
type DeliveryStore interface {
	Create(ctx context.Context, d *Delivery) error
	Get(ctx context.Context, id string) (*Delivery, error)
	Update(ctx context.Context, d *Delivery) error
	ListPending(ctx context.Context) ([]*Delivery, error)
}

// Searcher is the optional interface for semantic/keyword search.
type Searcher interface {
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
}

// SearchQuery defines a search request.
type SearchQuery struct {
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
}

// SearchResult is a scored search hit.
type SearchResult struct {
	Asset *Asset  `json:"asset"`
	Score float64 `json:"score"`
}


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

// ExternalURL returns the external URL (delegates to SourceURL).
func (m *Asset) ExternalURL() string { return m.SourceURL }

// SetExternalURL sets the external URL (delegates to SourceURL).
func (m *Asset) SetExternalURL(v string) { m.SourceURL = v }

// DriveFileID returns the Drive file ID from metadata.
func (m *Asset) DriveFileID() string { return m.GetMetadataString("drive_file_id") }
func (m *Asset) SetDriveFileID(v string) { m.SetMetadataString("drive_file_id", v) }

// DriveLink returns the Drive web link from metadata.
func (m *Asset) DriveLink() string { return m.GetMetadataString("drive_link") }
func (m *Asset) SetDriveLink(v string) { m.SetMetadataString("drive_link", v) }

// DownloadLink returns the download link from metadata.
func (m *Asset) DownloadLink() string { return m.GetMetadataString("download_link") }
func (m *Asset) SetDownloadLink(v string) { m.SetMetadataString("download_link", v) }

// LocalPath returns the local filesystem path from metadata.
func (m *Asset) LocalPath() string { return m.GetMetadataString("local_path") }
func (m *Asset) SetLocalPath(v string) { m.SetMetadataString("local_path", v) }

// FileHash returns the file hash from metadata.
func (m *Asset) FileHash() string { return m.GetMetadataString("file_hash") }
func (m *Asset) SetFileHash(v string) { m.SetMetadataString("file_hash", v) }

// FolderID returns the Drive folder ID from metadata.
func (m *Asset) FolderID() string { return m.GetMetadataString("folder_id") }
func (m *Asset) SetFolderID(v string) { m.SetMetadataString("folder_id", v) }

// FolderPath returns the folder path from metadata.
func (m *Asset) FolderPath() string { return m.GetMetadataString("folder_path") }
func (m *Asset) SetFolderPath(v string) { m.SetMetadataString("folder_path", v) }

// ParentFolderID returns the parent folder ID from metadata.
func (m *Asset) ParentFolderID() string { return m.GetMetadataString("parent_folder_id") }
func (m *Asset) SetParentFolderID(v string) { m.SetMetadataString("parent_folder_id", v) }

// Depth returns the tree depth from metadata.
func (m *Asset) Depth() int { return m.GetMetadataInt("depth") }
func (m *Asset) SetDepth(v int) { m.SetMetadataInt("depth", v) }

// IsFolder returns whether this is a folder node from metadata.
func (m *Asset) IsFolder() bool {
	if m.Metadata == nil { return false }
	v, ok := m.Metadata["is_folder"]
	if !ok { return false }
	b, _ := v.(bool)
	return b
}
func (m *Asset) SetIsFolder(v bool) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["is_folder"] = v
}

// ChildCount returns the child count from metadata.
func (m *Asset) ChildCount() int { return m.GetMetadataInt("child_count") }
func (m *Asset) SetChildCount(v int) { m.SetMetadataInt("child_count", v) }

// SceneType returns the scene type from metadata.
func (m *Asset) SceneType() string { return m.GetMetadataString("scene_type") }
func (m *Asset) SetSceneType(v string) { m.SetMetadataString("scene_type", v) }

// QualityScore returns the quality score from metadata.
func (m *Asset) QualityScore() float64 {
	if m.Metadata == nil { return 0 }
	v, ok := m.Metadata["quality_score"]
	if !ok { return 0 }
	switch val := v.(type) {
	case float64: return val
	case json.Number: f, _ := val.Float64(); return f
	default: return 0
	}
}
func (m *Asset) SetQualityScore(v float64) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["quality_score"] = v
}

// ReuseCount returns the reuse count from metadata.
func (m *Asset) ReuseCount() int { return m.GetMetadataInt("reuse_count") }
func (m *Asset) SetReuseCount(v int) { m.SetMetadataInt("reuse_count", v) }

// LastUsedAt returns the last-used timestamp from metadata.
func (m *Asset) LastUsedAt() string { return m.GetMetadataString("last_used_at") }
func (m *Asset) SetLastUsedAt(v string) { m.SetMetadataString("last_used_at", v) }

// UsableFor returns the usable-for tags from metadata.
func (m *Asset) UsableFor() []string {
	if m.Metadata == nil { return nil }
	v, ok := m.Metadata["usable_for"]
	if !ok { return nil }
	switch arr := v.(type) {
	case []string: return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok { result[i] = s }
		}
		return result
	}
	return nil
}
func (m *Asset) SetUsableFor(v []string) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["usable_for"] = v
}

// AvoidFor returns the avoid-for tags from metadata.
func (m *Asset) AvoidFor() []string {
	if m.Metadata == nil { return nil }
	v, ok := m.Metadata["avoid_for"]
	if !ok { return nil }
	switch arr := v.(type) {
	case []string: return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok { result[i] = s }
		}
		return result
	}
	return nil
}
func (m *Asset) SetAvoidFor(v []string) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["avoid_for"] = v
}

// PHash returns the perceptual hash from metadata.
func (m *Asset) PHash() string { return m.GetMetadataString("phash") }
func (m *Asset) SetPHash(v string) { m.SetMetadataString("phash", v) }

// EmbeddingJSON returns the embedding JSON from metadata.
func (m *Asset) EmbeddingJSON() string { return m.GetMetadataString("embedding_json") }
func (m *Asset) SetEmbeddingJSON(v string) { m.SetMetadataString("embedding_json", v) }

// VisualEmbedding returns the visual embedding from metadata.
func (m *Asset) VisualEmbedding() string { return m.GetMetadataString("visual_embedding") }
func (m *Asset) SetVisualEmbedding(v string) { m.SetMetadataString("visual_embedding", v) }

// TranscriptEmbedding returns the transcript embedding from metadata.
func (m *Asset) TranscriptEmbedding() string { return m.GetMetadataString("transcript_embedding") }
func (m *Asset) SetTranscriptEmbedding(v string) { m.SetMetadataString("transcript_embedding", v) }

// VisualEmbeddingJSON returns the visual embedding JSON from metadata.
func (m *Asset) VisualEmbeddingJSON() string { return m.GetMetadataString("visual_embedding_json") }
func (m *Asset) SetVisualEmbeddingJSON(v string) { m.SetMetadataString("visual_embedding_json", v) }




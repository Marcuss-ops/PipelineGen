package assets

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when an asset, artifact, or delivery is not found.
var ErrNotFound = errors.New("not found")

// ── LifecycleState ──────────────────────────────────────────────────

type LifecycleState string

const (
	StateStaging    LifecycleState = "STAGING"
	StateProcessing LifecycleState = "PROCESSING"
	StateActive     LifecycleState = "ACTIVE"
	StateDeleted    LifecycleState = "DELETED"
)

// ── Asset ───────────────────────────────────────────────────────────

// Asset represents the canonical model of a media asset in PipelineGen.
type Asset struct {
	ID                  string            `json:"id"`
	Source              string            `json:"source"` // "youtube", "artlist", "stock", "image", "local"
	Name                string            `json:"name"`
	Filename            string            `json:"filename"`
	MediaType           string            `json:"media_type"` // "video", "audio", "image"
	Category            string            `json:"category"`
	Group               string            `json:"group"`
	SourceURL           string            `json:"source_url"`
	ClipPageURL         string            `json:"clip_page_url"`
	ThumbnailURL        string            `json:"thumbnail_url"`
	DurationMs          int64             `json:"duration_ms"`
	Tags                []string          `json:"tags"`
	SearchTerms         []string          `json:"search_terms"`
	SearchText          string            `json:"search_text"`
	LifecycleState      LifecycleState    `json:"lifecycle_state"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	DeletedAt           *time.Time        `json:"deleted_at,omitempty"`
	Metadata            map[string]any    `json:"metadata"`

	// Physical location fields (persisted in asset_locations/metadata)
	LocalPath           string            `json:"local_path,omitempty"`
	DriveFileID         string            `json:"drive_file_id,omitempty"`
	DriveLink           string            `json:"drive_link,omitempty"`
	DownloadLink        string            `json:"download_link,omitempty"`
	FileHash            string            `json:"file_hash,omitempty"`
	FolderID            string            `json:"folder_id,omitempty"`
	ParentFolderID      string            `json:"parent_folder_id,omitempty"`
	FolderPath          string            `json:"folder_path,omitempty"`

	// Embedding fields
	EmbeddingJSON       string            `json:"embedding_json,omitempty"`
	VisualEmbedding     string            `json:"visual_embedding,omitempty"`
	TranscriptEmbedding string            `json:"transcript_embedding,omitempty"`

	// Domain-specific metadata
	PHash               string            `json:"phash,omitempty"`
	SceneType           string            `json:"scene_type,omitempty"`
	QualityScore        float64           `json:"quality_score,omitempty"`
	ReuseCount          int               `json:"reuse_count,omitempty"`
	LastUsedAt          string            `json:"last_used_at,omitempty"`
}

// Filter is used to query assets.
type Filter struct {
	Source   string
	Category string
	Group    string
	Limit    int
	Offset   int
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

// ── Store Interfaces ────────────────────────────────────────────────

// Store represents the unified CRUD repository for assets.
type Store interface {
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Save(ctx context.Context, asset *Asset) error
	Delete(ctx context.Context, id string) error
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



package asset

import "time"

// ArtifactStatus tracks the lifecycle of a stored artifact.
type ArtifactStatus string

const (
	ArtifactStaging     ArtifactStatus = "STAGING"
	ArtifactVerifying   ArtifactStatus = "VERIFYING"
	ArtifactReady       ArtifactStatus = "READY"
	ArtifactFailed      ArtifactStatus = "FAILED"
	ArtifactQuarantined ArtifactStatus = "QUARANTINED"
	ArtifactDeleted     ArtifactStatus = "DELETED"
)

// Artifact represents a generated or ingested binary (video, audio, image, etc.).
type Artifact struct {
	ID             string         `json:"id"`
	JobID          string         `json:"job_id,omitempty"`
	Kind           string         `json:"kind"`
	Status         ArtifactStatus `json:"status"`
	StorageBackend string         `json:"storage_backend"`
	StorageKey     string         `json:"storage_key"`
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

// Package artifacts provides content-addressed artifact storage and lifecycle
// management for PipelineGen. Artifacts are the output of render/processing
// jobs (videos, audio, thumbnails). They flow through a state machine:
// STAGING → VERIFYING → READY/FAILED/QUARANTINED.
//
// Storage is content-addressed via SHA-256: the canonical key for any blob
// is artifacts/sha256/xx/xxxx... where xx is the first two hex chars.
package artifacts

import (
	"context"
	"io"
	"time"
)

// Status represents the canonical artifact states.
type Status string

const (
	StatusStaging     Status = "STAGING"
	StatusVerifying   Status = "VERIFYING"
	StatusReady       Status = "READY"
	StatusFailed      Status = "FAILED"
	StatusQuarantined Status = "QUARANTINED"
	StatusDeleted     Status = "DELETED"
)

// Artifact is the domain model for a stored artifact.
type Artifact struct {
	ID             string    `json:"id"`
	JobID          string    `json:"job_id,omitempty"`
	Kind           string    `json:"kind"`           // video, audio, thumbnail, image
	Status         Status    `json:"status"`
	StorageBackend string    `json:"storage_backend"` // local, s3
	StorageKey     string    `json:"storage_key"`     // canonical blob path
	SHA256         string    `json:"sha256"`
	SizeBytes      int64     `json:"size_bytes"`
	MimeType       string    `json:"mime_type"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
}

// BlobStore is the abstraction over content-addressed blob storage.
// Implementations: LocalBlobStore (filesystem), S3BlobStore (future).
type BlobStore interface {
	// Stage writes the contents of r to a temporary staging area and returns
	// a staging key. The caller should pass this key to VerifyAndPromote.
	Stage(ctx context.Context, hint string) (StagingWriter, error)

	// VerifyAndPromote computes SHA-256 of the staged blob, moves it to its
	// canonical content-addressed location, and returns the canonical key.
	// Returns an error if the hash doesn't match an optional expected value.
	VerifyAndPromote(ctx context.Context, stagingKey string, expectedSHA256 string) (PromoteResult, error)

	// Open returns a reader for the blob at the given canonical key.
	Open(ctx context.Context, storageKey string) (io.ReadCloser, error)

	// Delete removes a blob from storage.
	Delete(ctx context.Context, storageKey string) error

	// Stat returns metadata about a stored blob.
	Stat(ctx context.Context, storageKey string) (BlobInfo, error)
}

// StagingWriter provides a writable handle during the staging phase.
// The caller must call Close() to finalize the write.
type StagingWriter interface {
	io.WriteCloser
	// Key returns the staging key for this upload.
	Key() string
}

// PromoteResult holds the outcome of staging → canonical promotion.
type PromoteResult struct {
	StorageKey string
	SHA256     string
	SizeBytes  int64
}

// BlobInfo holds metadata about a stored blob.
type BlobInfo struct {
	SHA256    string
	SizeBytes int64
	Exists    bool
}

// Repository is the persistence contract for artifact metadata.
type Repository interface {
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status Status, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]Artifact, error)
}

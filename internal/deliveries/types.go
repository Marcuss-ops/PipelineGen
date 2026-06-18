// Package deliveries provides multi-provider delivery of artifacts to
// external targets (Drive, YouTube, S3). Each delivery is an attempt to
// push a READY artifact to a configured target provider.
//
// Lifecycle: PENDING → LEASED → RUNNING → SUCCEEDED/FAILED/RETRY_WAIT.
package deliveries

import (
	"context"
	"io"
	"time"
)

// Status represents the canonical delivery states.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusLeased     Status = "LEASED"
	StatusRunning    Status = "RUNNING"
	StatusRetryWait  Status = "RETRY_WAIT"
	StatusSucceeded  Status = "SUCCEEDED"
	StatusFailed     Status = "FAILED"
	StatusBlockedAuth Status = "BLOCKED_AUTH"
	StatusCancelled  Status = "CANCELLED"
)

// Delivery is a concrete attempt to push an artifact to a target.
type Delivery struct {
	ID              string     `json:"id"`
	ArtifactID      string     `json:"artifact_id"`
	TargetID        string     `json:"target_id"`
	Provider        string     `json:"provider"` // drive, youtube, s3
	Status          Status     `json:"status"`
	AttemptCount    int        `json:"attempt_count"`
	MaxAttempts     int        `json:"max_attempts"`
	NextAttemptAt   *time.Time `json:"next_attempt_at,omitempty"`
	LeaseID         string     `json:"lease_id,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	RemoteID        string     `json:"remote_id,omitempty"`
	RemoteURL       string     `json:"remote_url,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// Provider is the interface that delivery providers must implement.
// Each provider handles one external service (Drive, YouTube, S3).
type Provider interface {
	// Name returns the provider identifier (e.g. "drive", "youtube").
	Name() string

	// Deliver pushes the artifact content to the target and returns
	// the remote identifier and URL on success.
	Deliver(ctx context.Context, req Request) (Result, error)

	// ClassifyError categorizes an error for retry policy decisions.
	ClassifyError(err error) FailureClass
}

// Request holds the data needed by a provider to execute a delivery.
type Request struct {
	DeliveryID  string
	ArtifactID  string
	StorageKey  string
	SHA256      string
	SizeBytes   int64
	MimeType    string
	LocalPath   string // temporary; will be removed when providers stream from BlobStore
	OpenReader  func(ctx context.Context) (io.ReadCloser, error)
}

// Result holds the outcome of a successful delivery.
type Result struct {
	RemoteID  string
	RemoteURL string
}

// FailureClass categorizes delivery errors for retry policy.
type FailureClass int

const (
	FailureTemporary  FailureClass = iota // retryable (timeout, 429, 5xx)
	FailurePermanent                       // non-retryable (invalid config, not found)
	FailureAuth                            // auth issue (token revoked)
)

// Repository is the persistence contract for delivery records.
type Repository interface {
	Create(ctx context.Context, d *Delivery) error
	Get(ctx context.Context, id string) (*Delivery, error)
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error)
	UpdateStatus(ctx context.Context, id string, status Status, remoteID, remoteURL, lastError string) error
	RenewLease(ctx context.Context, id, leaseID string, leaseTTL time.Duration) error
	RequeueStale(ctx context.Context, now time.Time, limit int) ([]Delivery, error)
	ListByArtifact(ctx context.Context, artifactID string) ([]Delivery, error)
}

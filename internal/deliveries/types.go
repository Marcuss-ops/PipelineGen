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
	StatusPending     Status = "PENDING"
	StatusLeased      Status = "LEASED"
	StatusRunning     Status = "RUNNING"
	StatusRetryWait   Status = "RETRY_WAIT"
	StatusSucceeded   Status = "SUCCEEDED"
	StatusFailed      Status = "FAILED"
	StatusBlockedAuth Status = "BLOCKED_AUTH"
	StatusCancelled   Status = "CANCELLED"
)

// Delivery is a concrete attempt to push an artifact to a target.
type Delivery struct {
	ID               string     `json:"id"`
	ArtifactID       string     `json:"artifact_id"`
	DestinationID    string     `json:"destination_id"`
	Provider         string     `json:"provider"` // drive, youtube, s3
	Status           Status     `json:"status"`
	AttemptCount     int        `json:"attempt_count"`
	MaxAttempts      int        `json:"max_attempts"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	LockedBy         string     `json:"locked_by,omitempty"`
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	RemoteID         string     `json:"remote_id,omitempty"`
	RemoteURL        string     `json:"remote_url,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorMessage string     `json:"last_error_message,omitempty"`
	IdempotencyKey   string     `json:"idempotency_key,omitempty"`
	StorageKey       string     `json:"storage_key,omitempty"`
	SHA256           string     `json:"sha256,omitempty"`
	SizeBytes        int64      `json:"size_bytes,omitempty"`
	MimeType         string     `json:"mime_type,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`

	// Legacy fields kept for backward compat during migration.
	LeaseID        string     `json:"-"`
	LeaseExpiresAt *time.Time `json:"-"`
	TargetID       string     `json:"-"`
	LastError      string     `json:"-"`
}

// DeliveryDestination represents a configured delivery target.
type DeliveryDestination struct {
	DestinationID string `json:"destination_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Enabled       bool   `json:"enabled"`
	ConfigJSON    string `json:"config_json,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// IsTerminal returns true for terminal states.
func (s Status) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// ── ArtifactReader (storage-independent provider input) ──────────────

// ArtifactReader provides streaming access to artifact content.
// Providers use this instead of filesystem paths, so they remain
// decoupled from the BlobStore's storage layout.
type ArtifactReader interface {
	Open(ctx context.Context, storageKey string) (io.ReadCloser, error)
	Stat(ctx context.Context, storageKey string) (ObjectInfo, error)
}

// ObjectInfo holds artifact metadata for delivery.
type ObjectInfo struct {
	SHA256    string
	SizeBytes int64
	MimeType  string
}

// ArtifactDescriptor carries the artifact identity for a provider.
type ArtifactDescriptor struct {
	ArtifactID string
	StorageKey string
	ObjectInfo
}

// ── Provider interface ───────────────────────────────────────────────

// Provider is the interface that delivery providers must implement.
type Provider interface {
	Name() string
	Deliver(ctx context.Context, artifact ArtifactDescriptor, content ArtifactReader, destination DeliveryDestination) (*Result, error)
	ClassifyError(err error) FailureClass
}

// Result holds the outcome of a successful delivery.
type Result struct {
	RemoteID  string
	RemoteURL string
}

// FailureClass categorizes delivery errors for retry policy.
type FailureClass int

const (
	FailureTemporary FailureClass = iota // retryable (timeout, 429, 5xx)
	FailurePermanent                     // non-retryable (invalid config, not found)
	FailureAuth                          // auth issue (token revoked)
)

// ── Typed Command Structs (atomic operations) ────────────────────────

// CompleteDeliveryCommand marks a delivery SUCCEEDED.
type CompleteDeliveryCommand struct {
	DeliveryID string
	LockedBy   string
	RemoteID   string
	RemoteURL  string
}

// FailDeliveryCommand marks a delivery FAILED.
type FailDeliveryCommand struct {
	DeliveryID       string
	LockedBy         string
	ErrorCode        string
	ErrorMessage     string
}

// RetryDeliveryCommand sets a delivery to RETRY_WAIT with backoff.
type RetryDeliveryCommand struct {
	DeliveryID   string
	LockedBy     string
	NextAttemptAt time.Time
	ErrorCode    string
	ErrorMessage string
}

// BlockAuthCommand marks a delivery BLOCKED_AUTH.
type BlockAuthCommand struct {
	DeliveryID   string
	LockedBy     string
	ErrorCode    string
	ErrorMessage string
}

// ── Repository interface ─────────────────────────────────────────────

// Repository is the persistence contract for delivery records.
type Repository interface {
	Create(ctx context.Context, d *Delivery) error
	Get(ctx context.Context, id string) (*Delivery, error)
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error)
	RenewLease(ctx context.Context, id, lockedBy string, leaseTTL time.Duration) error
	RequeueStale(ctx context.Context, now time.Time, limit int) ([]Delivery, error)
	ListByArtifact(ctx context.Context, artifactID string) ([]Delivery, error)

	// Atomic outcome operations (single transaction: delivery + attempt + event).
	CompleteDelivery(ctx context.Context, cmd CompleteDeliveryCommand) error
	FailDelivery(ctx context.Context, cmd FailDeliveryCommand) error
	RetryDelivery(ctx context.Context, cmd RetryDeliveryCommand) error
	BlockDeliveryAuth(ctx context.Context, cmd BlockAuthCommand) error

	// Idempotency support.
	FindByIdempotencyKey(ctx context.Context, key string) (*Delivery, error)
	UpsertDestination(ctx context.Context, dest *DeliveryDestination) error
	GetDestination(ctx context.Context, destinationID string) (*DeliveryDestination, error)
}

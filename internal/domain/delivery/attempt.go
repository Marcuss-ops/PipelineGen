package delivery

import "time"

// Delivery represents an attempt to deliver an artifact to a destination.
type Delivery struct {
	ID               string         `json:"id"`
	ArtifactID       string         `json:"artifact_id"`
	DestinationID    string         `json:"destination_id"`
	Provider         string         `json:"provider"`
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

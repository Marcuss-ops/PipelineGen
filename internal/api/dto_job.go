// Package dto provides Data Transfer Objects for the HTTP API layer.
// These are public contracts for handlers and clients; they are independent
// of the domain types in internal/core/domain/.
package api

import (
	"encoding/json"
)

// JobResponse is the canonical JSON response for a single job.
// It is the HTTP-facing contract and is independent of the underlying
// domain models in internal/core/domain/job/ and the legacy models in
// internal/media/models/.
type JobResponse struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Progress      int             `json:"progress"`
	Priority      int             `json:"priority,omitempty"`
	Project       string          `json:"project,omitempty"`
	Error         string          `json:"error,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	RetryCount    int             `json:"retry_count"`
	MaxRetries    int             `json:"max_retries"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

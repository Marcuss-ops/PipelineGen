// Package veloxclient is a minimal HTTP client for submitting jobs to a
// pipelinegen server. This file holds shared types and sentinel errors.
package veloxclient

import (
	"errors"
	"time"
)

// AsyncResponse is the enqueue response from async endpoints.
type AsyncResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// JobStatusResponse mirrors GET /api/jobs/{ID}/full.
type JobStatusResponse struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"`
	Type     string         `json:"type"`
	Progress int            `json:"progress"`
	Error    string         `json:"error,omitempty"`
	Result   map[string]any `json:"result,omitempty"`
}

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// IsTerminal returns true if the status will not transition further.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

var (
	ErrUnauthorized = errors.New("veloxclient: unauthorized (rotate token)")
	ErrBadRequest   = errors.New("veloxclient: bad request (do not retry)")
	ErrServer       = errors.New("veloxclient: server error (surface to operator)")
	ErrNotFound     = errors.New("veloxclient: job not found")
)

const DefaultMaxAttempts = 3
const DefaultRetryBase = 200 * time.Millisecond

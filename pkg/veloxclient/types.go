// Package veloxclient is a minimal HTTP client for submitting jobs to a
// pipelinegen server. This file holds shared types and sentinel errors.
package veloxclient

import (
	"encoding/json"
	"errors"
	"time"
)

// AsyncResponse is the enqueue response from async endpoints.
type AsyncResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// UnmarshalJSON decodes BOTH shapes the same endpoint has been observed to
// answer with for `job_id`:
//
//	"job_id": "job_1789_abc"          // canonical transport.EnqueueAsync ACK
//	"job_id": {"id": "job_1789_abc"} // older deployed build: the job itself
//
// Observed live (2026-09-20): a deployed server answered
// POST /api/clips/process with the object form. The job WAS enqueued, but
// every `velox submit` failed with
//
//	decode response: json: cannot unmarshal object into Go struct field
//	AsyncResponse.job_id of type string
//
// so the operator was told the submission failed while the work was already
// queued — and the job id (the only handle on that work) was thrown away.
// Tolerating both shapes keeps the CLI usable against either build instead of
// coupling its usefulness to which binary happens to be deployed.
func (a *AsyncResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		JobID  json.RawMessage `json:"job_id"`
		Status string          `json:"status"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.JobID = ""
	a.Status = raw.Status
	if len(raw.JobID) == 0 || string(raw.JobID) == "null" {
		return nil
	}
	if raw.JobID[0] == '"' {
		return json.Unmarshal(raw.JobID, &a.JobID)
	}
	var carrier struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw.JobID, &carrier); err != nil {
		return err
	}
	a.JobID = carrier.ID
	return nil
}

// M2MJobTypesResponse is the runnable job catalog exposed to remote clients.
type M2MJobTypesResponse struct {
	Types []string `json:"types"`
}

// JobStatusResponse mirrors the enriched status response returned by both
// GET /api/jobs/{ID}/full and GET /api/v1/jobs/{ID}.
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

var (
	ErrUnauthorized = errors.New("veloxclient: unauthorized (rotate token)")
	ErrBadRequest   = errors.New("veloxclient: bad request (do not retry)")
	ErrServer       = errors.New("veloxclient: server error (surface to operator)")
	ErrNotFound     = errors.New("veloxclient: job not found")
	// ErrNotReady is returned when the server answers 409 Conflict: the
	// resource exists but is not available yet (e.g. a clip whose render job
	// is still RUNNING). It is deliberately distinct from ErrNotFound so
	// callers can retry instead of treating the asset as missing.
	ErrNotReady = errors.New("veloxclient: resource not ready (retry)")
)

const DefaultMaxAttempts = 3
const DefaultRetryBase = 200 * time.Millisecond

func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

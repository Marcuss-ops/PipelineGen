package job

import (
	"encoding/json"
	"time"
)

// WorkerCapabilities is the worker registration payload stored by the
// authoritative API. Job-type capabilities are intentionally explicit so
// the broker can reject incompatible claims before any work starts.
type WorkerCapabilities struct {
	JobTypes []string `json:"job_types,omitempty"`
	CPUCores int      `json:"cpu_cores,omitempty"`
	RAMMB    int      `json:"ram_mb,omitempty"`
	GPU      bool     `json:"gpu,omitempty"`
	FFmpeg   bool     `json:"ffmpeg,omitempty"`
	Whisper  bool     `json:"whisper,omitempty"`
}

type RegisterWorkerCommand struct {
	WorkerID       string             `json:"worker_id"`
	Name           string             `json:"name,omitempty"`
	Version        string             `json:"version,omitempty"`
	Hostname       string             `json:"hostname,omitempty"`
	Capabilities   WorkerCapabilities `json:"capabilities"`
	CorrelationID  string             `json:"correlation_id,omitempty"`
	SessionTTL     time.Duration      `json:"session_ttl,omitempty"`
}

type ClaimCommand struct {
	WorkerID         string   `json:"worker_id"`
	WorkerSessionID  string   `json:"worker_session_id"`
	JobID            string   `json:"job_id,omitempty"`
	LeaseID          string   `json:"lease_id,omitempty"`
	ExpectedRevision int      `json:"expected_revision,omitempty"`
	CorrelationID    string   `json:"correlation_id,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	WaitSeconds      int      `json:"wait_seconds,omitempty"`
}

type HeartbeatCommand struct {
	WorkerID        string        `json:"worker_id"`
	WorkerSessionID string        `json:"worker_session_id"`
	CorrelationID   string        `json:"correlation_id,omitempty"`
	SessionTTL      time.Duration `json:"session_ttl,omitempty"`
}

type RenewCommand struct {
	WorkerID         string        `json:"worker_id"`
	WorkerSessionID  string        `json:"worker_session_id"`
	JobID            string        `json:"job_id"`
	LeaseID          string        `json:"lease_id"`
	ExpectedRevision int           `json:"expected_revision"`
	CorrelationID    string        `json:"correlation_id,omitempty"`
	LeaseTTL         time.Duration `json:"lease_ttl,omitempty"`
}

type ProgressCommand struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	JobID            string          `json:"job_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Progress         int             `json:"progress"`
	Message          string          `json:"message,omitempty"`
	Data             json.RawMessage `json:"data,omitempty"`
}

type CompleteCommand struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	JobID            string          `json:"job_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
}

type FailCommand struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	JobID            string          `json:"job_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Error            string          `json:"error"`
}

package job

import (
	"encoding/json"
	"time"
)

// WorkerSession identifies a registered worker identity. A new registration
// always produces a new session ID. Returned by Broker.RegisterWorker and
// required as a credential on every subsequent worker call.
type WorkerSession struct {
	WorkerID         string             `json:"worker_id"`
	SessionID        string             `json:"session_id"`
	SessionExpiresAt time.Time          `json:"session_expires_at"`
	Capabilities     WorkerCapabilities `json:"capabilities"`
	Version          string             `json:"version"`
	Hostname         string             `json:"hostname"`
}

// WorkerCapabilities describes what a registered worker is able to handle.
// job.Job-type capabilities are intentionally explicit so the broker can
// reject incompatible claims before any work starts.
type WorkerCapabilities struct {
	JobTypes []string `json:"job_types,omitempty"`
	CPUCores int      `json:"cpu_cores,omitempty"`
	RAMMB    int      `json:"ram_mb,omitempty"`
	GPU      bool     `json:"gpu,omitempty"`
	FFmpeg   bool     `json:"ffmpeg,omitempty"`
	Whisper  bool     `json:"whisper,omitempty"`
}

// RegisterWorkerCommand is the payload a worker sends when (re)registering
// itself with the broker. SessionTTL is a hint; the broker may cap it.
type RegisterWorkerCommand struct {
	WorkerID      string             `json:"worker_id"`
	Name          string             `json:"name,omitempty"`
	Version       string             `json:"version,omitempty"`
	Hostname      string             `json:"hostname,omitempty"`
	Capabilities  WorkerCapabilities `json:"capabilities"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	SessionTTL    time.Duration      `json:"session_ttl,omitempty"`
}

// ClaimCommand requests a job lease from the broker. JobID/LeaseID are
// scope hints — empty means "any available eligible job".
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

// HeartbeatCommand keeps a worker session alive and may extend SessionTTL.
type HeartbeatCommand struct {
	WorkerID        string        `json:"worker_id"`
	WorkerSessionID string        `json:"worker_session_id"`
	CorrelationID   string        `json:"correlation_id,omitempty"`
	SessionTTL      time.Duration `json:"session_ttl,omitempty"`
}

// RenewCommand extends a held lease without giving up the job.
type RenewCommand struct {
	WorkerID         string        `json:"worker_id"`
	WorkerSessionID  string        `json:"worker_session_id"`
	JobID            string        `json:"job_id"`
	LeaseID          string        `json:"lease_id"`
	ExpectedRevision int           `json:"expected_revision"`
	CorrelationID    string        `json:"correlation_id,omitempty"`
	LeaseTTL         time.Duration `json:"lease_ttl,omitempty"`
}

// ProgressCommand reports partial progress on a held lease.
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

// CompleteCommand marks a held job as successfully finished.
type CompleteCommand struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	JobID            string          `json:"job_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
}

// FailCommand marks a held job as failed with a terminal error message.
type FailCommand struct {
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobID            string `json:"job_id"`
	LeaseID          string `json:"lease_id"`
	ExpectedRevision int    `json:"expected_revision"`
	CorrelationID    string `json:"correlation_id,omitempty"`
	Error            string `json:"error"`
}

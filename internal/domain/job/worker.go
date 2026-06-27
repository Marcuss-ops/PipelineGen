package job

import (
	"encoding/json"
	"time"
)

// WorkerSession identifies a registered worker identity. A new registration
// always produces a new session ID. Returned by Broker.RegisterWorker and
// required as a credential on every subsequent worker call.
//
// Hardware is the LAST OBSERVED WorkerHardwareStats payload from the
// worker's heartbeat. nil = worker has not reported hardware telemetry
// yet (typical for stale sessions). Production callers should treat a
// nil Hardware as "data not yet available", not as an error.
type WorkerSession struct {
	WorkerID         string               `json:"worker_id"`
	SessionID        string               `json:"session_id"`
	SessionExpiresAt time.Time            `json:"session_expires_at"`
	Capabilities     WorkerCapabilities   `json:"capabilities"`
	Version          string               `json:"version"`
	Hostname         string               `json:"hostname"`
	Hardware         *WorkerHardwareStats `json:"hardware,omitempty"`
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
//
// Hardware is the worker-side telemetry snapshot taken immediately
// before sending the heartbeat. The broker caches the latest value
// onto the session row (WorkerSession.Hardware) so an admin operator
// running the cert-report endpoint can inspect the worker's live
// /proc-derived state. nil = no telemetry this heartbeat.
type HeartbeatCommand struct {
	WorkerID        string               `json:"worker_id"`
	WorkerSessionID string               `json:"worker_session_id"`
	CorrelationID   string               `json:"correlation_id,omitempty"`
	SessionTTL      time.Duration        `json:"session_ttl,omitempty"`
	Hardware        *WorkerHardwareStats `json:"hardware,omitempty"`
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

// WorkerHardwareStats is the canonical sampling output of
// pkg/workerstats.Sample. It is the SINGLE AUTHORITATIVE POJO for
// per-worker Linux /proc telemetry — the sampler, the heartbeat
// command, the broker session, the admin cert-report endpoint, and
// the observability metric-emission site ALL project this struct
// verbatim (no field copies, no JSON-tag renames).
//
// Units rule (verbatim; mirrors pkg/workerstats/stats.go package doc):
//   - Raw bytes (uint64) for storage + network fields.
//   - 0.0-1.0 ratio (float32) for CPU usage fields.
//   - No seconds-vs-bytes confusion. Timestamps in Unix milliseconds.
//
// Drift between this godoc and the sampler's units documentation is a
// reading-order hazard for operators; both must agree.
type WorkerHardwareStats struct {
	// SampledAtUnixMs is the wall-clock time (UnixMilli) at which
	// pkg/workerstats.Sample populated the struct. Cross-server stable.
	SampledAtUnixMs int64 `json:"sampled_at_unix_ms"`

	// CPUUsageRatio is (user+nice+system) / (busy + idle) over the
	// /proc/stat aggregate "cpu " line, range 0.0-1.0. iowait /
	// irq / softirq / steal / guest slices are intentionally EXCLUDED
	// from both numerator and denominator — they are operator-readable
	// directly in /proc/stat and would otherwise drive the ratio to
	// noisy extremes on iowait-dominated hosts. The derivable busy
	// fraction under load is therefore conservative.
	CPUUsageRatio float32 `json:"cpu_usage_ratio"`

	// Memory population (runtime.ReadMemStats).
	MemoryAllocBytes uint64 `json:"memory_alloc_bytes"`
	MemorySysBytes   uint64 `json:"memory_sys_bytes"`
	MemoryHeapBytes  uint64 `json:"memory_heap_bytes"`
	MemoryNumGC      uint32 `json:"memory_num_gc"`

	// Network byte counters (sum across non-loopback
	// /proc/net/dev interfaces, or filtered to NetworkDevice).
	NetRxBytes uint64 `json:"net_rx_bytes"`
	NetTxBytes uint64 `json:"net_tx_bytes"`

	// Disk occupancy from syscall.Statfs on the worker's
	// DiskMountPath. Reserved tails are folded into DiskUsedBytes so
	// the free/used split is operator-meaningful, not kernel-internal.
	DiskFreeBytes uint64 `json:"disk_free_bytes"`
	DiskUsedBytes uint64 `json:"disk_used_bytes"`
}

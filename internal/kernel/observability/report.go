package observability

import (
	"encoding/json"
	"time"
)

// Canonical run statuses. SUCCEEDED / FAILED / CANCELLED are the terminal
// vocabulary; RUNNING is the in-flight state set by StartRun.
const (
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusCancelled = "CANCELLED"
)

// Stage and operation statuses. Lower-case to match the kernel/job
// StageStatus vocabulary (completed/failed/skipped).
const (
	StageStatusRunning   = "running"
	StageStatusCompleted = "completed"
	StageStatusFailed    = "failed"
)

// RunReport is the canonical serialisable observability document for one job
// execution. It is what every consumer (job result summary, /timings
// endpoint, Prometheus adapter, SQLite writer) reads.
type RunReport struct {
	RunID       string `json:"run_id"`
	JobID       string `json:"job_id"`
	JobType     string `json:"job_type"`
	AttemptID   string `json:"attempt_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`

	Status string `json:"status"`

	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	QueueWaitMs int64 `json:"queue_wait_ms"`
	WallTimeMs  int64 `json:"wall_time_ms"`
	ActiveMs    int64 `json:"active_ms"`
	BlockedMs   int64 `json:"blocked_ms,omitempty"`
	// AccumulatedOperationMs is the sum of all operation durations. Under
	// real parallelism it exceeds WallTimeMs (4 parallel 6s downloads →
	// ≈24000ms vs ≈6000ms), which is the parallelism diagnostic.
	AccumulatedOperationMs int64 `json:"accumulated_operation_ms,omitempty"`

	Stages     []StageReport     `json:"stages,omitempty"`
	Operations []OperationReport `json:"operations,omitempty"`
	Artifacts  []ArtifactReport  `json:"artifacts,omitempty"`
	Counters   RunCounters       `json:"counters,omitempty"`
	Children   *ChildrenSummary  `json:"children,omitempty"`

	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

// StageReport is the report of one canonical pipeline stage.
type StageReport struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Attempts   int    `json:"attempts"`

	CacheStatus string `json:"cache_status,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`

	ItemsInput     int64 `json:"items_input,omitempty"`
	ItemsCompleted int64 `json:"items_completed,omitempty"`
	ItemsFailed    int64 `json:"items_failed,omitempty"`
	BytesProcessed int64 `json:"bytes_processed,omitempty"`
}

// OperationReport is the report of one external-boundary operation.
type OperationReport struct {
	Stage     string `json:"stage"`
	Component string `json:"component"`
	Operation string `json:"operation"`
	Provider  string `json:"provider,omitempty"`

	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Attempts   int    `json:"attempts"`

	Items int64 `json:"items,omitempty"`
	Bytes int64 `json:"bytes,omitempty"`

	CacheStatus string `json:"cache_status,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
}

// ArtifactReport records one produced or reused output of a run.
type ArtifactReport struct {
	// Kind is the canonical artifact class (drive_file, local_file,
	// document, image, voiceover, ...).
	Kind string `json:"kind"`
	// Ref is the stable artifact reference (Drive file ID, doc ID, ...).
	Ref string `json:"ref,omitempty"`
	// URL is the public link when available.
	URL string `json:"url,omitempty"`
	// Stage names the pipeline stage that produced the artifact.
	Stage string `json:"stage,omitempty"`
	// Bytes is the artifact size in bytes.
	Bytes int64 `json:"bytes,omitempty"`
	// Reused marks a cache reuse instead of a fresh production.
	Reused bool `json:"reused,omitempty"`
}

// RunCounters aggregates universal run counters. These are persisted in the
// report and logs; they are NOT Prometheus labels (high cardinality).
type RunCounters struct {
	ItemsRequested int64 `json:"items_requested,omitempty"`
	ItemsCompleted int64 `json:"items_completed,omitempty"`
	ItemsFailed    int64 `json:"items_failed,omitempty"`
	CacheHits      int64 `json:"cache_hits,omitempty"`
	CacheMisses    int64 `json:"cache_misses,omitempty"`
	Retries        int64 `json:"retries,omitempty"`

	BytesDownloaded int64 `json:"bytes_downloaded,omitempty"`
	BytesUploaded   int64 `json:"bytes_uploaded,omitempty"`

	ArtifactsCreated int64 `json:"artifacts_created,omitempty"`
	ArtifactsReused  int64 `json:"artifacts_reused,omitempty"`
}

// ChildrenSummary aggregates the linked child runs of a parent run. Durations
// are summed, never naively averaged, so the wall time of parallel children
// is the sum of the individual children walls.
type ChildrenSummary struct {
	Requested  int   `json:"requested"`
	Completed  int   `json:"completed"`
	Failed     int   `json:"failed"`
	WallTimeMs int64 `json:"wall_time_ms,omitempty"`
}

// JSON serialises the report. Convenience over json.Marshal for report
// consumers (timing summary, /timings endpoint, persistence writer).
func (r *RunReport) JSON() ([]byte, error) {
	return json.Marshal(r)
}

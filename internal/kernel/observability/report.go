package observability

import (
	"encoding/json"
	"time"
)

const (
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusCancelled = "CANCELLED"
	StatusAbandoned = "ABANDONED"
)

const (
	StageStatusRunning   = "running"
	StageStatusCompleted = "completed"
	StageStatusFailed    = "failed"
)

type RunReport struct {
	RunID                  string            `json:"run_id"`
	JobID                  string            `json:"job_id"`
	JobType                string            `json:"job_type"`
	AttemptID              string            `json:"attempt_id,omitempty"`
	LeaseID                string            `json:"lease_id,omitempty"`
	ParentRunID            string            `json:"parent_run_id,omitempty"`
	ParentJobID            string            `json:"parent_job_id,omitempty"`
	WorkerID               string            `json:"worker_id,omitempty"`
	LeaseExpiresAt         time.Time         `json:"lease_expires_at,omitempty"`
	Status                 string            `json:"status"`
	ObservabilityDegraded  bool              `json:"observability_degraded,omitempty"`
	CreatedAt              time.Time         `json:"created_at"`
	StartedAt              time.Time         `json:"started_at"`
	FinishedAt             time.Time         `json:"finished_at"`
	QueueWaitMs            int64             `json:"queue_wait_ms"`
	WallTimeMs             int64             `json:"wall_time_ms"`
	ActiveMs               int64             `json:"active_ms"`
	BlockedMs              int64             `json:"blocked_ms,omitempty"`
	AccumulatedOperationMs int64             `json:"accumulated_operation_ms,omitempty"`
	Stages                 []StageReport     `json:"stages,omitempty"`
	Operations             []OperationReport `json:"operations,omitempty"`
	Artifacts              []ArtifactReport  `json:"artifacts,omitempty"`
	Counters               RunCounters       `json:"counters,omitempty"`
	Children               *ChildrenSummary  `json:"children,omitempty"`
	ErrorCode              string            `json:"error_code,omitempty"`
	Error                  string            `json:"error,omitempty"`
}

type StageReport struct {
	ObservationID  string    `json:"observation_id"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
	DurationMs     int64     `json:"duration_ms"`
	Attempts       int       `json:"attempts"`
	CacheStatus    string    `json:"cache_status,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
	ItemsInput     int64     `json:"items_input,omitempty"`
	ItemsCompleted int64     `json:"items_completed,omitempty"`
	ItemsFailed    int64     `json:"items_failed,omitempty"`
	BytesProcessed int64     `json:"bytes_processed,omitempty"`
}

type OperationReport struct {
	ObservationID string `json:"observation_id"`
	Stage         string `json:"stage"`
	Component     string `json:"component"`
	Operation     string `json:"operation"`
	Provider      string `json:"provider,omitempty"`
	Status        string `json:"status"`
	DurationMs    int64  `json:"duration_ms"`
	Attempts      int    `json:"attempts"`
	Items         int64  `json:"items,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	CacheStatus   string `json:"cache_status,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
}

type ArtifactReport struct {
	ObservationID string `json:"observation_id,omitempty"`
	Kind          string `json:"kind"`
	Ref           string `json:"ref,omitempty"`
	URL           string `json:"url,omitempty"`
	Stage         string `json:"stage,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	Reused        bool   `json:"reused,omitempty"`
}

type RunCounters struct {
	ItemsRequested   int64 `json:"items_requested,omitempty"`
	ItemsCompleted   int64 `json:"items_completed,omitempty"`
	ItemsFailed      int64 `json:"items_failed,omitempty"`
	CacheHits        int64 `json:"cache_hits,omitempty"`
	CacheMisses      int64 `json:"cache_misses,omitempty"`
	Retries          int64 `json:"retries,omitempty"`
	BytesDownloaded  int64 `json:"bytes_downloaded,omitempty"`
	BytesUploaded    int64 `json:"bytes_uploaded,omitempty"`
	ArtifactsCreated int64 `json:"artifacts_created,omitempty"`
	ArtifactsReused  int64 `json:"artifacts_reused,omitempty"`
}

type ChildrenSummary struct {
	Requested  int   `json:"requested"`
	Completed  int   `json:"completed"`
	Failed     int   `json:"failed"`
	WallTimeMs int64 `json:"wall_time_ms,omitempty"`
}

func (r *RunReport) JSON() ([]byte, error) { return json.Marshal(r) }

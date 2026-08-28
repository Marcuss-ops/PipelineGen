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

type BatchReport struct {
	BatchWallMs        int64   `json:"batch_wall_ms"`
	BatchWorkMs        int64   `json:"batch_work_ms"`
	PeakConcurrency    int     `json:"peak_concurrency"`
	AverageConcurrency float64 `json:"average_concurrency"`
	ParallelismFactor  float64 `json:"parallelism_factor"`
	// SlotUtilization is peak_concurrency / configured slots — how much of
	// the configured pool was ever busy at once. Omitted (absent) when the
	// configured slot count is unknown: never a fake zero.
	SlotUtilization float64 `json:"slot_utilization,omitempty"`
	// ScalingEfficiency is parallelism_factor / configured slots — the
	// fraction of the ideal perfectly-parallel speedup actually achieved.
	// Omitted when the configured slot count is unknown.
	ScalingEfficiency float64 `json:"scaling_efficiency,omitempty"`
	// Phases carries the per-phase reconstructed concurrency (peak/avg) for
	// every operation name with timed observations in the batch.
	Phases map[string]PhaseConcurrency `json:"phases,omitempty"`
}

// DeriveBatchReport derives batch metrics solely from attempt-owned RunReports.
// Intervals use StartedAt/FinishedAt; no benchmark clock is involved.
// configuredSlots is the pool the batch was bounded to (0 = unknown); it
// enables the derived slot_utilization and scaling_efficiency aggregates.
// All concurrency is reconstructed through the canonical sweep
// (sweepConcurrency) with the deterministic end-before-start tie-breaker, so
// the report never depends on the input ordering of the runs.
func DeriveBatchReport(reports []RunReport, configuredSlots int) BatchReport {
	var out BatchReport
	if len(reports) == 0 {
		return out
	}
	var minStart, maxFinish time.Time
	for _, r := range reports {
		start := r.StartedAt
		finish := r.FinishedAt
		if start.IsZero() || finish.IsZero() || finish.Before(start) {
			continue
		}
		if minStart.IsZero() || start.Before(minStart) {
			minStart = start
		}
		if maxFinish.IsZero() || finish.After(maxFinish) {
			maxFinish = finish
		}
		out.BatchWorkMs += r.ExecutionWallMs
	}
	if minStart.IsZero() || maxFinish.IsZero() {
		return out
	}
	out.BatchWallMs = maxFinish.Sub(minStart).Milliseconds()
	points := make([]concurrencyPoint, 0, len(reports)*2)
	for _, r := range reports {
		if r.StartedAt.IsZero() || r.FinishedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
			continue
		}
		points = append(points,
			concurrencyPoint{at: r.StartedAt, delta: 1},
			concurrencyPoint{at: r.FinishedAt, delta: -1})
	}
	out.PeakConcurrency, out.AverageConcurrency = sweepConcurrency(points, out.BatchWallMs)
	if out.BatchWallMs > 0 {
		out.ParallelismFactor = float64(out.BatchWorkMs) / float64(out.BatchWallMs)
	}
	if configuredSlots > 0 && out.BatchWallMs > 0 {
		out.SlotUtilization = float64(out.PeakConcurrency) / float64(configuredSlots)
		out.ScalingEfficiency = out.ParallelismFactor / float64(configuredSlots)
	}
	out.Phases = derivePhaseConcurrency(reports)
	return out
}

type RunReport struct {
	RunID                 string    `json:"run_id"`
	JobID                 string    `json:"job_id"`
	JobType               string    `json:"job_type"`
	AttemptID             string    `json:"attempt_id"`
	LeaseID               string    `json:"lease_id,omitempty"`
	ParentRunID           string    `json:"parent_run_id,omitempty"`
	ParentJobID           string    `json:"parent_job_id,omitempty"`
	WorkerID              string    `json:"worker_id,omitempty"`
	LeaseExpiresAt        time.Time `json:"lease_expires_at,omitempty"`
	Status                string    `json:"status"`
	ObservabilityDegraded bool      `json:"observability_degraded,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	StartedAt             time.Time `json:"started_at"`
	FinishedAt            time.Time `json:"finished_at"`
	QueueWaitMs           int64     `json:"queue_wait_ms"`
	// ExecutionWallMs is the attempt wall after claim. It is the canonical
	// runtime execution duration; WallTimeMs remains the compatibility name.
	ExecutionWallMs        int64             `json:"execution_wall_ms"`
	WallTimeMs             int64             `json:"wall_time_ms"`
	BlockedMs              int64             `json:"blocked_ms,omitempty"`
	AccumulatedOperationMs int64             `json:"accumulated_operation_ms,omitempty"`
	AttributedStageMs      int64             `json:"attributed_stage_ms"`
	UnattributedMs         int64             `json:"unattributed_ms"`
	UnattributedPercent    float64           `json:"unattributed_percent"`
	BottleneckStage        string            `json:"bottleneck_stage,omitempty"`
	BottleneckOperation    string            `json:"bottleneck_operation,omitempty"`
	Stages                 []StageReport     `json:"stages,omitempty"`
	Operations             []OperationReport `json:"operations,omitempty"`
	Artifacts              []ArtifactReport  `json:"artifacts,omitempty"`
	Counters               RunCounters       `json:"counters,omitempty"`
	Waits                  []WaitReport      `json:"waits,omitempty"`
	Children               *ChildrenSummary  `json:"children,omitempty"`
	KPIs                   PipelineKPIs      `json:"kpis,omitempty"`
	ClipTimeline           *ClipTimeline     `json:"clip_timeline,omitempty"`
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
	ObservationID    string    `json:"observation_id"`
	Stage            string    `json:"stage"`
	Component        string    `json:"component"`
	Operation        string    `json:"operation"`
	Provider         string    `json:"provider,omitempty"`
	Status           string    `json:"status"`
	DurationMs       int64     `json:"duration_ms"`
	QueueWaitMs      int64     `json:"queue_wait_ms,omitempty"`
	Attempts         int       `json:"attempts"`
	Items            int64     `json:"items,omitempty"`
	Bytes            int64     `json:"bytes,omitempty"`
	CacheStatus      string    `json:"cache_status,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	SourceSHA256     string    `json:"source_sha256,omitempty"`
	SourceDurationMS int64     `json:"source_duration_ms,omitempty"`
	SourceSizeBytes  int64     `json:"source_size_bytes,omitempty"`
	OutputDurationMS int64     `json:"output_duration_ms,omitempty"`
	OutputSizeBytes  int64     `json:"output_size_bytes,omitempty"`
	CPUUserMS        int64     `json:"cpu_user_ms,omitempty"`
	CPUSystemMS      int64     `json:"cpu_system_ms,omitempty"`
	Width            int       `json:"width,omitempty"`
	Height           int       `json:"height,omitempty"`
	FPS              float64   `json:"fps,omitempty"`
	InputCodec       string    `json:"input_codec,omitempty"`
	OutputCodec      string    `json:"output_codec,omitempty"`
	CacheHit         bool      `json:"cache_hit,omitempty"`
	Strategy         string    `json:"strategy,omitempty"`
	MetadataJSON     string    `json:"metadata_json,omitempty"`
	WorkerID         string    `json:"worker_id,omitempty"`
	QueuedAt         time.Time `json:"queued_at,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	CreatedAt        string    `json:"created_at,omitempty"`
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

type WaitReport struct {
	Kind       WaitKind  `json:"kind"`
	Component  string    `json:"component,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMs int64     `json:"duration_ms"`
}

type ChildrenSummary struct {
	Requested          int   `json:"requested"`
	Completed          int   `json:"completed"`
	Failed             int   `json:"failed"`
	AccumulatedChildMs int64 `json:"accumulated_child_ms,omitempty"`
}

// PipelineKPIs carries the canonical pipeline key performance indicators
// measured on the Run's own clock. Every field is a duration in milliseconds
// relative to the run's StartedAt. Zero means the milestone was not reached.
type PipelineKPIs struct {
	// GenerateFirstSceneReadyMs is the wall offset when the first scene
	// text was emitted by the SceneTextReady coordinator (streaming mode)
	// or when the generate phase finished (serial mode).
	GenerateFirstSceneReadyMs int64 `json:"generate_first_scene_ready_ms"`
	// GenerateFinishedMs is the wall offset when the scene text generation
	// phase completed.
	GenerateFinishedMs int64 `json:"generate_finished_ms"`
	// TTSFirstStartedMs is the wall offset when the first TTS synthesis
	// request was dispatched to the provider.
	TTSFirstStartedMs int64 `json:"tts_first_started_ms"`
	// RenderFirstStartedMs is the wall offset when the first localized
	// render was enqueued.
	RenderFirstStartedMs int64 `json:"render_first_started_ms"`
	// AudioCompileStartedMs is the wall offset when the audio compile
	// phase began.
	AudioCompileStartedMs int64 `json:"audio_compile_started_ms"`
	// AudioCompileFinishedMs is the wall offset when the audio compile
	// phase completed.
	AudioCompileFinishedMs int64 `json:"audio_compile_finished_ms"`
	// DocsPublishStartedMs is the wall offset when the document publish
	// phase began.
	DocsPublishStartedMs int64 `json:"docs_publish_started_ms"`
	// DocsPublishFinishedMs is the wall offset when the document publish
	// phase completed.
	DocsPublishFinishedMs int64 `json:"docs_publish_finished_ms"`

	// Invariants (computed at finish time)
	InvariantRenderBeforeGenerateFinished bool `json:"invariant_render_before_generate_finished"`
	InvariantTTSNeverWaitsRender          bool `json:"invariant_tts_never_waits_render"`
	InvariantTTSNeverWaitsDrive           bool `json:"invariant_tts_never_waits_drive"`
	InvariantUnattributedBelowFivePercent bool `json:"invariant_unattributed_below_five_percent"`
}

func (r *RunReport) JSON() ([]byte, error) { return json.Marshal(r) }

// JSON returns the versioned resource envelope as canonical JSON.
func (r *RunResourceReport) JSON() ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return json.Marshal(r)
}

// UnmarshalJSON accepts the retired child wall-time key for persisted report
// compatibility while always exposing the canonical AccumulatedChildMs field.
func (r *ChildrenSummary) UnmarshalJSON(data []byte) error {
	type childSummary ChildrenSummary
	var raw struct {
		Requested          int   `json:"requested"`
		Completed          int   `json:"completed"`
		Failed             int   `json:"failed"`
		AccumulatedChildMs int64 `json:"accumulated_child_ms"`
		WallTimeMs         int64 `json:"wall_time_ms"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ChildrenSummary(childSummary{Requested: raw.Requested, Completed: raw.Completed, Failed: raw.Failed, AccumulatedChildMs: raw.AccumulatedChildMs})
	if r.AccumulatedChildMs == 0 {
		r.AccumulatedChildMs = raw.WallTimeMs
	}
	return nil
}

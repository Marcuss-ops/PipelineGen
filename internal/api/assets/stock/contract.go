package stock

import stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"

// runRequest is the JSON body for POST /api/stock-pipeline/run.
type runRequest struct {
	SearchQueries                  []string                          `json:"search_queries"`
	DirectURLs                     []string                          `json:"direct_urls,omitempty"`
	DriveURLs                      []string                          `json:"drive_urls,omitempty"`
	Clips                          []stockpipeline.ClipSpec          `json:"clips,omitempty"`
	TotalMinutes                   int                               `json:"total_minutes"`
	TargetTotalDurationSeconds     int                               `json:"target_total_duration_seconds,omitempty"`
	TargetDurationPerSourceSeconds int                               `json:"target_duration_per_source_seconds,omitempty"`
	ClipsPerSource                 int                               `json:"clips_per_source,omitempty"`
	ClipDurationSeconds            int                               `json:"clip_duration_seconds,omitempty"`
	DownloadMode                   string                            `json:"download_mode,omitempty"`
	ChunkDuration                  int                               `json:"chunk_duration,omitempty"`
	ClipDuration                   int                               `json:"clip_duration,omitempty"`
	SecondsPerSegment              int                               `json:"seconds_per_segment,omitempty"`
	NoAudio                        bool                              `json:"no_audio,omitempty"`
	NoEffects                      bool                              `json:"no_effects,omitempty"`
	NoTransitions                  bool                              `json:"no_transitions,omitempty"`
	MaxVideos                      int                               `json:"max_videos,omitempty"`
	Subfolder                      string                            `json:"subfolder"`
	FolderName                     string                            `json:"folder_name"`
	DriveFolderID                  string                            `json:"drive_folder_id,omitempty"`
	FolderID                       string                            `json:"folder_id,omitempty"`
	Metadata                       *stockpipeline.ChunkMetadataInput `json:"metadata,omitempty"`
	Async                          bool                              `json:"async,omitempty"`
	Persist                        bool                              `json:"persist,omitempty"`
}

// runResponse is the JSON response for POST /api/stock-pipeline/run and
// POST /api/stock-pipeline/search-and-run.
// godlike/06 SSOT: all error responses carry a machine-readable
// `error_code` field (UNKNOWN_FIELD / INVALID_URL / PATH_TRAVERSAL /
// MAX_CLIPS_EXCEEDED / INVALID_PAYLOAD). Successful responses carry
// `deduplicated` (always present, default false) — the idempotency
// followup will flip it to true when a duplicate run is detected.
type runResponse struct {
	JobID        string `json:"job_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	Status       string `json:"status"`
	Deduplicated bool   `json:"deduplicated"`
	Error        string `json:"error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

// Constants for the HTTP contract — exported so the test suite can
// reference them without copy/paste drift.
const (
	// MaxClipsPerRun is the upper bound on `clips` per single
	// request. Larger jobs MUST be split client-side into multiple
	// runs (the orchestrator flags 100+ clips as a misuse surface).
	MaxClipsPerRun = 100

	// MaxURLLength caps individual URL strings to defense-in-depth
	// against URL-flood DoS. 2048 chars covers long Drive-share links
	// with auth tokens; longer URLs are flagged for operator review
	// and should be wrapped in a separate reference.
	MaxURLLength = 2048

	// Response-level status strings (godlike/06 SSOT decoupling):
	// these describe the *endpoint acknowledgement* — not the broker
	// job state enum (QUEUED / RUNNING / FINALIZING / SUCCEEDED /
	// INDEX_PENDING, owned by internal/kernel/job.Status).
	//   - StatusPending = request accepted, work scheduled via the
	//     jobs broker (async path; useCase.Submit returned a jobID).
	//     Callers poll /api/jobs/{id}/full or wait for the
	//     broker-level terminal state to know the actual outcome.
	//   - StatusCompleted = request processed inline (sync path;
	//     useCase.Submit returned with empty jobID, e.g. test
	//     fixture or partial-deploy worker). The job is finished
	//     by the time the response is serialised; no follow-up
	//     polling is required.
	// Keeping these distinct from the broker enum avoids the silent-
	// confusion class where a "QUEUED" status string at the endpoint
	// implies "job not yet started" while the broker is in RUNNING.
	StatusPending   = "QUEUED"
	StatusCompleted = "completed"
	// StatusError is the third endpoint-acknowledgement value —
	// emitted on every 4xx/5xx response (validation rejections, broker
	// unavailability). The `error_code` field carries the machine-
	// readable subtype (UNKNOWN_FIELD / INVALID_URL / etc.); `status`
	// stays at the canonical literal so clients can branch on a single
	// field with no enum drift.
	StatusError = "error"

	// Error codes — machine-readable tags for client retry logic.
	ErrCodeUnknownField   = "UNKNOWN_FIELD"
	ErrCodeInvalidURL     = "INVALID_URL"
	ErrCodePathTraversal  = "PATH_TRAVERSAL"
	ErrCodeMaxClips       = "MAX_CLIPS_EXCEEDED"
	ErrCodeInvalidPayload = "INVALID_PAYLOAD"
)

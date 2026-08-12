// Package jobregistry defines the durable execution ledger contract.
// SQLite is the source of truth; reports are derived from these rows.
package jobregistry

import "context"

type Job struct {
	JobID, JobType, Status                string
	CorrelationID, ParentJobID, RootJobID string
	ProjectID, VideoID                    string
	PayloadJSON, PayloadHash              string
	ResultJSON, ErrorCode, ErrorMessage   string
	GitSHA, AppVersion, WorkerID, Host    string
	CreatedAt, StartedAt, CompletedAt     string
	DurationMS                            int64
}

type Step struct {
	StepID, JobID, StepName, StepType, Status                               string
	StartedAt, CompletedAt, ErrorCode, ErrorMessage, MetricsJSON, CreatedAt string
	DurationMS, InputCount, OutputCount, InputBytes, OutputBytes            int64
}

type Metric struct {
	MetricID, JobID, StepID, Name, Unit, CreatedAt string
	Value                                          float64
}

type AssetRelation struct {
	JobID, AssetID, Relation, StepID, CreatedAt string
	Ordinal                                     int
}

type Event struct {
	EventID, JobID, EventType, PayloadJSON, CreatedAt string
}

type StepSummary struct {
	StepName      string  `json:"step"`
	Runs          int     `json:"runs"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	P95DurationMS float64 `json:"p95_duration_ms"`
}

type Stats struct {
	From                string        `json:"from,omitempty"`
	To                  string        `json:"to,omitempty"`
	Jobs                int           `json:"jobs"`
	Successful          int           `json:"successful"`
	Failed              int           `json:"failed"`
	ScriptsGenerated    int           `json:"scripts_generated"`
	ClipsDownloaded     int           `json:"clips_downloaded"`
	ImagesGenerated     int           `json:"images_generated"`
	VoiceoversGenerated int           `json:"voiceovers_generated"`
	VideosRendered      int           `json:"videos_rendered"`
	AvgPipelineMS       float64       `json:"avg_pipeline_ms"`
	SlowestSteps        []StepSummary `json:"slowest_steps,omitempty"`
}

type Registry interface {
	RecordJob(context.Context, Job) error
	UpdateJob(context.Context, Job) error
	RecordStep(context.Context, Step) error
	RecordMetric(context.Context, Metric) error
	RelateAsset(context.Context, AssetRelation) error
	AppendEvent(context.Context, Event) (int64, error)
	Stats(context.Context, string, string) (Stats, error)
}

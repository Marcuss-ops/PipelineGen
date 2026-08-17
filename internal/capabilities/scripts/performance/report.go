package performance

// PhaseMeasurement is one resolved phase of a single job report. Measured is
// false when the canonical source for the phase was absent or zero, in which
// case DurationMS is 0 — the report surfaces the gap instead of estimating it.
type PhaseMeasurement struct {
	Phase      PerformancePhase   `json:"phase"`
	Source     string             `json:"source"`
	DurationMS int64              `json:"duration_ms"`
	Measured   bool               `json:"measured"`
	Counters   map[string]float64 `json:"counters,omitempty"`
}

// UnmeasuredPhase names a phase whose canonical source was absent for a job.
// It is surfaced explicitly (with its source) so the missing instrumentation is
// visible at a glance instead of being inferred from zero durations.
type UnmeasuredPhase struct {
	Phase  PerformancePhase `json:"phase"`
	Source string           `json:"source"`
}

// WaitMeasurement projects one typed wait interval from the canonical report.
type WaitMeasurement struct {
	Kind       string `json:"kind"`
	Component  string `json:"component,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// WaitSummary aggregates the canonical wait signals. It stays out of the
// phase list on purpose: waiting is not CPU/pipeline work and must not inflate
// a phase total.
type WaitSummary struct {
	QueueMS          int64             `json:"queue_ms"`
	BlockedMS        int64             `json:"blocked_ms"`
	CompletionMS     int64             `json:"completion_ms"`
	OutboxDeliveryMS int64             `json:"outbox_delivery_ms"`
	Items            []WaitMeasurement `json:"items,omitempty"`
}

// AudioSummary is the audio-specific summary projected from the canonical
// AudioPipelineMetrics.
type AudioSummary struct {
	DurationMS int64   `json:"duration_ms"`
	RTF        float64 `json:"rtf,omitempty"`
	TTSCalls   int     `json:"tts_calls"`
	TTSScenes  int     `json:"tts_scenes"`
}

// ScriptSummary splits the SCRIPT execution step into provider inference and
// script overhead. TotalMS is the SCRIPT execution step duration;
// InferenceMS is the sum of the Ollama "generate" operations (the actual
// inference the provider owns); OverheadMS is TotalMS minus InferenceMS
// (checkpoint, scene-commit emission, input-attach overhead), clamped at zero.
type ScriptSummary struct {
	TotalMS     int64 `json:"total_ms"`
	InferenceMS int64 `json:"inference_ms"`
	OverheadMS  int64 `json:"overhead_ms"`
}

// PerformanceReport is the read-only per-job performance view.
type PerformanceReport struct {
	JobID      string             `json:"job_id"`
	WallTimeMS int64              `json:"wall_ms"`
	Script     ScriptSummary      `json:"script"`
	Phases     []PhaseMeasurement `json:"phases"`
	Unmeasured []UnmeasuredPhase  `json:"unmeasured,omitempty"`
	Waits      WaitSummary        `json:"waits"`
	Audio      AudioSummary       `json:"audio"`
}

// PhaseStats aggregates one phase across several jobs. MeasuredJobs is the
// number of jobs whose canonical source actually populated the phase; a phase
// with zero measured jobs keeps zero-value stats rather than fabricating a
// number.
type PhaseStats struct {
	Phase        PerformancePhase `json:"phase,omitempty"`
	MeasuredJobs int              `json:"measured_jobs"`
	MinMS        int64            `json:"min_ms"`
	MedianMS     int64            `json:"median_ms"`
	AvgMS        float64          `json:"avg_ms"`
	P95MS        int64            `json:"p95_ms"`
	MaxMS        int64            `json:"max_ms"`
	PctWall      float64          `json:"pct_wall,omitempty"`
}

// AggregatePerformanceReport compares several jobs. Wall aggregates the
// wall_time_ms column; PctWall on each phase is that phase's average divided
// by the wall average (times 100), so phases can be ranked against total wall
// time without double-counting waits.
type AggregatePerformanceReport struct {
	JobIDs     []string           `json:"job_ids"`
	Wall       PhaseStats         `json:"wall"`
	Phases     []PhaseStats       `json:"phases"`
	Unmeasured []PerformancePhase `json:"unmeasured,omitempty"`
}

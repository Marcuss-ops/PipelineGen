package adapters

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TimingParity is the bounded comparison record used while legacy timing
// writers are being retired. The canonical duration is always authoritative;
// LegacyMs is diagnostic input only and is never used to alter the report.
type TimingParity struct {
	Name        string
	CanonicalMs int64
	LegacyMs    int64
	Match       bool
}

// TimingParitySink receives non-invasive dual-read comparisons.
type TimingParitySink interface {
	ObserveTimingParity(TimingParity)
}

// CanonicalTimingAdapter is the compatibility bridge for the old timing
// surfaces. MeasureCanonical is the only production timing entry point. Once
// a Run is bound to ctx it delegates to the kernel and projects that exact
// observation to StageDurations, VidRushTimingMetrics, and processmetrics.
type CanonicalTimingAdapter struct {
	ProcessMetrics processmetrics.CanonicalRecorder
	VidRush        VidRushTimingMetrics
	Parity         TimingParitySink
}

// MeasureCanonical executes fn under the canonical Run timer. With no Run in
// the context it deliberately passes through without inventing a duration;
// callers may retain their pre-migration fallback for uninstrumented tests or
// legacy entry points.
func (a *CanonicalTimingAdapter) MeasureCanonical(ctx context.Context, name string, fn func(context.Context) error) (kernobs.StageReport, error) {
	return kernobs.MeasureStageReport(ctx, kernobs.StageName(name), fn)
}

// ProjectStage copies one canonical observation to the compatibility sinks.
// StageReport.DurationMs is the sole duration value used by every projection.
// It does not claim parity because no independent legacy value was read.
func (a *CanonicalTimingAdapter) ProjectStage(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport) {
	a.projectStage(ctx, result, name, stage)
}

// ProjectStageWithLegacy compares an independently supplied legacy value with
// the canonical observation. The legacy value is diagnostic only and never
// replaces the canonical duration.
func (a *CanonicalTimingAdapter) ProjectStageWithLegacy(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport, legacyMs int64) {
	a.projectStage(ctx, result, name, stage)
	if a == nil || a.Parity == nil {
		return
	}
	a.Parity.ObserveTimingParity(TimingParity{
		Name: name, CanonicalMs: stage.DurationMs, LegacyMs: legacyMs,
		Match: stage.DurationMs == legacyMs,
	})
}

func (a *CanonicalTimingAdapter) projectStage(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport) {
	if result != nil {
		if result.StageDurations == nil {
			result.StageDurations = make(map[string]int64)
		}
		result.StageDurations[name] = stage.DurationMs
	}
	if a == nil {
		return
	}
	if a.VidRush != nil {
		a.VidRush.ObserveProcessorDuration(name, float64(stage.DurationMs)/1000)
	}
	if a.ProcessMetrics != nil {
		_ = a.ProcessMetrics.RecordCanonical(ctx, processmetrics.CanonicalMetric{
			ProcessType: "script",
			JobID:       stageJobID(ctx),
			ParentJobID: stageParentJobID(ctx),
			Phase:       name,
			Provider:    "script",
			StartedAt:   stage.StartedAt,
			DurationMs:  stage.DurationMs,
			Status:      canonicalMetricStatus(stage.Status),
			ErrorCode:   stage.ErrorCode,
			CreatedAt:   stage.FinishedAt,
		})
	}
}

func stageJobID(ctx context.Context) string {
	if run := kernobs.FromContext(ctx); run != nil {
		if report := run.Report(); report != nil {
			return report.JobID
		}
	}
	return ""
}

func stageParentJobID(ctx context.Context) string {
	if run := kernobs.FromContext(ctx); run != nil {
		if report := run.Report(); report != nil {
			return report.ParentJobID
		}
	}
	return ""
}

func canonicalMetricStatus(status string) string {
	if status == kernobs.StageStatusFailed {
		return "failure"
	}
	return "success"
}

// LegacyStageDuration is retained for callers that still need a fallback
// measurement outside a canonical Run. It is intentionally isolated from
// MeasureCanonical so production runs cannot accidentally use both timers.
func LegacyStageDuration(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

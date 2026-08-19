package multilingual

// recorder.go — Recorder confluences multilingual-render measurements into the
// canonical durable performance registry (performance_runs / performance_steps
// / performance_operations). It is NOT a second metrics system: it writes the
// exact tables + ports already owned by internal/capabilities/performance and
// internal/platform/sqlite/performance (the same sink the ObservedExecutor and
// the job projector use).
//
// One multilingual-render job → one performance_runs row + one
// performance_steps row per measured phase (backfill, translate_cues, ass,
// render). Each per-clip-language operation → one performance_operations row
// (multilingual.translate / multilingual.ass / multilingual.render) carrying
// source identity, elapsed ms, output bytes and cache-hit flag.

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// Recorder is the canonical metrics collector for the multilingual render
// pipeline. Safe for a single run per process; both ports are mandatory.
type Recorder struct {
	registry capperformance.Registry
	ops      kernobs.OperationReportProjectionRecorder
	log      *zap.Logger
}

// NewRecorder constructs the collector. Fail-open on nil ports is NOT allowed
// at construction (the caller checks), but a nil port is tolerated at
// record time (metrics are best-effort and must never fail the render).
func NewRecorder(registry capperformance.Registry, ops kernobs.OperationReportProjectionRecorder, log *zap.Logger) *Recorder {
	if log == nil {
		log = zap.NewNop()
	}
	return &Recorder{registry: registry, ops: ops, log: log}
}

// ExecCounters certifies zero duplicate work: shared operations (download,
// probe, transcribe) run at most once, while fan-out operations (translate,
// ass, render, validate, upload) run once per language — never per-cue ×
// language, never twice per pipeline, never re-downloading the source per
// language.
type ExecCounters struct {
	Download   int64 `json:"download_exec_count"`
	Probe      int64 `json:"probe_exec_count"`
	Transcribe int64 `json:"transcribe_exec_count"`
	Translate  int64 `json:"translate_exec_count"`
	// TranslateFullText counts the separate full-text translation pass run by
	// the backfill Materializer (transcript pipeline). It is tracked separately
	// from Translate (per-cue) so a redundant second translation pass is
	// visible instead of silently hidden: the zero-duplicate target is
	// Translate == one-per-language and TranslateFullText == 0.
	TranslateFullText int64 `json:"translate_fulltext_exec_count"`
	ASS               int64 `json:"ass_exec_count"`
	Render            int64 `json:"render_exec_count"`
	Validate          int64 `json:"validate_exec_count"`
	Upload            int64 `json:"upload_exec_count"`
}

// StepMetrics is one measured phase for the performance_steps table.
type StepMetrics struct {
	Name         string
	DurationMS   int64
	InputCount   int64
	OutputCount  int64
	InputBytes   int64
	OutputBytes  int64
	CacheHits    int64
	CacheMisses  int64
	MetadataJSON string
}

// RunMetrics is the aggregate per-job measurement for one multilingual render.
// WallMS and SumOperationMS are intentionally SEPARATE: wall_ms is the real
// elapsed time, sum_operation_ms is the accumulated per-operation work — their
// ratio is the measured parallel speedup, never inferred from one number.
type RunMetrics struct {
	JobID       string
	WorkloadID  string
	StartedAt   time.Time
	CompletedAt time.Time

	WallMS       int64
	CPUUserMS    int64
	CPUSystemMS  int64
	PeakRSSBytes int64

	ClipCount    int
	SuccessCount int
	FailedCount  int

	TotalInputBytes  int64
	TotalOutputBytes int64

	CacheHits   int64
	CacheMisses int64

	WorkerLimit    int
	SumOperationMS int64

	// RenderConcurrency / TranslateConcurrency are the reconstructed real
	// parallelism of the two bounded fan-outs (configured vs observed workers,
	// average parallelism, queue latency).
	RenderConcurrency    observability.ConcurrencyStats
	TranslateConcurrency observability.ConcurrencyStats

	// Throughput is the derived batch benchmark projection (clips/min,
	// media-minutes/min, aggregate render RTF).
	Throughput Throughput

	// Operations are the actual execution counts (zero-duplicate certificate).
	Operations ExecCounters

	Steps []StepMetrics
}

// RecordRun persists the job-level run + one step per measured phase. The
// run_id is deterministic from the job id, so a re-record converges (upsert).
// Metrics write failures are logged, never returned — a metric write must not
// fail the render it measures.
func (r *Recorder) RecordRun(ctx context.Context, m RunMetrics) {
	if r == nil || r.registry == nil {
		return
	}
	// When called from a tracked execution, the canonical RunReport owns the
	// run identity and lifecycle timing. RunMetrics remains the compatibility
	// payload for benchmark-only fields and is never allowed to replace those
	// canonical facts.
	if run := kernobs.FromContext(ctx); run != nil {
		if report := run.Report(); report != nil {
			if report.JobID != "" {
				m.JobID = report.JobID
			}
			m.StartedAt = report.StartedAt
			m.CompletedAt = report.FinishedAt
			m.WallMS = report.WallTimeMs
			m.SumOperationMS = report.AccumulatedOperationMs
			if report.Status == kernobs.StatusFailed {
				m.FailedCount = maxInt(m.FailedCount, 1)
			}
		}
	}
	status := "SUCCEEDED"
	if m.FailedCount > 0 {
		status = "FAILED"
	}
	meta := runMetadata(m)
	run := capperformance.Run{
		RunID:        runID(m.JobID),
		JobID:        m.JobID,
		WorkloadID:   m.WorkloadID,
		Status:       status,
		WallMS:       m.WallMS,
		CPUUserMS:    m.CPUUserMS,
		CPUSystemMS:  m.CPUSystemMS,
		PeakRSSBytes: m.PeakRSSBytes,
		MetadataJSON: meta,
		StartedAt:    formatTime(m.StartedAt),
		CompletedAt:  formatTime(m.CompletedAt),
	}
	if err := r.registry.RecordRun(ctx, run); err != nil {
		r.log.Warn("multilingual metrics: record run", zap.Error(err))
	}
	for _, s := range m.Steps {
		step := capperformance.Step{
			StepID:       run.RunID + ":" + s.Name,
			RunID:        run.RunID,
			JobID:        m.JobID,
			Name:         s.Name,
			Status:       "SUCCEEDED",
			DurationMS:   s.DurationMS,
			InputCount:   s.InputCount,
			OutputCount:  s.OutputCount,
			InputBytes:   s.InputBytes,
			OutputBytes:  s.OutputBytes,
			CacheHits:    s.CacheHits,
			CacheMisses:  s.CacheMisses,
			MetadataJSON: s.MetadataJSON,
			StartedAt:    run.StartedAt,
			CompletedAt:  run.CompletedAt,
		}
		if err := r.registry.RecordStep(ctx, step); err != nil {
			r.log.Warn("multilingual metrics: record step", zap.String("step", s.Name), zap.Error(err))
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RecordOperation persists one per-clip-language operation measurement into
// performance_operations. Best-effort: a write failure is a logged warning.
func (r *Recorder) RecordOperation(ctx context.Context, m kernobs.MeasuredOperation) {
	if r == nil || r.ops == nil {
		return
	}
	// The canonical run owns the observation; performance storage is only a
	// read-model projection of the same measured fact.
	report := kernobs.OperationReportFromMeasuredOperation(m)
	m.ObservationID = report.ObservationID
	kernobs.RecordMeasuredOperation(ctx, m)
	if err := r.ops.RecordOperationReport(ctx, report); err != nil {
		r.log.Warn("multilingual metrics: record operation", zap.String("operation", m.Operation), zap.Error(err))
	}
}

func runID(jobID string) string {
	if jobID == "" {
		jobID = time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	return "multilingual-render:" + jobID
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// runMetadata embeds the non-column summary fields into performance_runs
// .metadata_json so the durable row carries the full parallelization picture
// without new columns: clip/success/failed counts, worker limit, and the
// sum-of-operations vs wall split.
func runMetadata(m RunMetrics) string {
	meta := struct {
		ClipCount            int                            `json:"clip_count"`
		SuccessCount         int                            `json:"success_count"`
		FailedCount          int                            `json:"failed_count"`
		WorkerLimit          int                            `json:"worker_limit"`
		SumOperationMS       int64                          `json:"sum_operation_ms"`
		TotalInput           int64                          `json:"total_input_bytes"`
		TotalOutput          int64                          `json:"total_output_bytes"`
		CacheHits            int64                          `json:"cache_hits"`
		CacheMisses          int64                          `json:"cache_misses"`
		RenderParallelism    observability.ConcurrencyStats `json:"render_parallelism"`
		TranslateParallelism observability.ConcurrencyStats `json:"translate_parallelism"`
		Throughput           Throughput                     `json:"throughput"`
		Operations           ExecCounters                   `json:"operations"`
	}{m.ClipCount, m.SuccessCount, m.FailedCount, m.WorkerLimit, m.SumOperationMS, m.TotalInputBytes, m.TotalOutputBytes, m.CacheHits, m.CacheMisses, m.RenderConcurrency, m.TranslateConcurrency, m.Throughput, m.Operations}
	b, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ProcessResources samples the CURRENT process's cumulative CPU time (user +
// system, in ms) and peak resident set size (bytes) from /proc. Returns zeros
// on any read/parse failure (non-Linux or permission denied): these metrics
// are best-effort and must never fail the run. Callers sample at start and end
// and diff the CPU values; PeakRSSBytes is a high-water mark read at the end.
func ProcessResources() (cpuUserMS, cpuSystemMS int64, peakRSSBytes int64) {
	// /proc/self/stat: utime/stime are fields 14/15 (1-indexed), AFTER the
	// parenthesised comm field (which may itself contain spaces/parens). Parse
	// from the LAST ')' so the comm content is skipped reliably.
	if data, err := os.ReadFile("/proc/self/stat"); err == nil {
		s := string(data)
		if idx := strings.LastIndexByte(s, ')'); idx >= 0 && idx+2 < len(s) {
			fields := strings.Fields(s[idx+2:])
			// fields[0]=state(field3) … fields[11]=utime(field14), fields[12]=stime(field15).
			if len(fields) > 12 {
				utime, err1 := strconv.ParseInt(fields[11], 10, 64)
				stime, err2 := strconv.ParseInt(fields[12], 10, 64)
				if err1 == nil && err2 == nil {
					// USER_HZ = 100 ticks/s on Linux → ms.
					cpuUserMS = utime * 1000 / 100
					cpuSystemMS = stime * 1000 / 100
				}
			}
		}
	}
	// /proc/self/status VmHWM = peak resident set (kB).
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmHWM:") {
				if parts := strings.Fields(line); len(parts) >= 2 {
					if kb, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						peakRSSBytes = kb * 1024
					}
				}
				break
			}
		}
	}
	return cpuUserMS, cpuSystemMS, peakRSSBytes
}

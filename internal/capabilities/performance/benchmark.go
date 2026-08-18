// Package performance — benchmark.go owns the fixed benchmark workloads and
// the median regression comparison run after every substantial renderer
// change. A workload is the durable definition (input identity + parameters
// + expected output); a suite runs each workload N times, records the
// median as a performance_run, and compares it against the recorded
// baseline (previous runs' wall time) so an optimization that improves one
// thing and regresses three others is caught by the numbers, not by eye.
//
// Workload definitions are pure; running them needs a WorkloadExecutor
// (the media boundary, e.g. the Rust renderer). Input/expected-output
// hashes are filled by the golden-input generation step once the canonical
// assets are materialized and hashed.
package performance

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Canonical workload ids and version.
const (
	WorkloadVersion       = "v1"
	Workload1080p10s      = "benchmark_1080p_10s"
	Workload1080p60s      = "benchmark_1080p_60s"
	WorkloadWatermark     = "benchmark_watermark"
	WorkloadAudioMix      = "benchmark_audio_mix"
	Workload10SceneRender = "benchmark_10_scene_render"
	WorkloadStreamCopy    = "benchmark_stream_copy"
)

// RegressionThresholdPercent is the |delta| below which a comparison is
// UNCHANGED: medians rarely differ by exactly 0, and sub-percent movement
// is noise, not signal.
const RegressionThresholdPercent = 1.0

// CanonicalWorkloads returns the fixed benchmark workloads. ParametersJSON
// is the operation + its canonical parameters; InputManifestSHA256 and
// ExpectedOutputSHA256 are populated when the canonical golden inputs are
// materialized and hashed (the benchmark input-generation step).
func CanonicalWorkloads() []Workload {
	return []Workload{
		{WorkloadID: Workload1080p10s, Version: WorkloadVersion, ParametersJSON: `{"operation":"normalize","width":1920,"height":1080,"fps":30,"duration_sec":10}`},
		{WorkloadID: Workload1080p60s, Version: WorkloadVersion, ParametersJSON: `{"operation":"normalize","width":1920,"height":1080,"fps":30,"duration_sec":60}`},
		{WorkloadID: WorkloadWatermark, Version: WorkloadVersion, ParametersJSON: `{"operation":"watermark","width":1920,"height":1080,"fps":30}`},
		{WorkloadID: WorkloadAudioMix, Version: WorkloadVersion, ParametersJSON: `{"operation":"audio_mix","codec":"aac","profile":"LC","sample_rate":48000,"channels":2}`},
		{WorkloadID: Workload10SceneRender, Version: WorkloadVersion, ParametersJSON: `{"operation":"render_scene","scene_count":10,"width":1920,"height":1080,"fps":30}`},
		{WorkloadID: WorkloadStreamCopy, Version: WorkloadVersion, ParametersJSON: `{"operation":"assemble_copy","codec":"h264","pixel_format":"yuv420p","fps":30}`},
	}
}

// MedianInt64 returns the median of the values (sorted internally). Empty
// input returns 0.
func MedianInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted)%2 == 1 {
		return float64(sorted[len(sorted)/2])
	}
	mid := len(sorted) / 2
	return (float64(sorted[mid-1]) + float64(sorted[mid])) / 2
}

// Comparison verdicts.
const (
	VerdictNoBaseline = "NO_BASELINE"
	VerdictImproved   = "IMPROVED"
	VerdictRegressed  = "REGRESSED"
	VerdictUnchanged  = "UNCHANGED"
)

// Comparison is one workload's regression result: the previous and current
// medians and the delta. DeltaPercent is positive when the current median
// is slower (regression) and negative when faster (improvement).
type Comparison struct {
	WorkloadID       string
	PreviousMedianMS float64
	CurrentMedianMS  float64
	DeltaPercent     float64
	Verdict          string
}

// CompareBaseline compares the current samples against the recorded
// baseline samples by median. No baseline → NO_BASELINE; |delta| below the
// regression threshold → UNCHANGED; faster → IMPROVED; slower → REGRESSED.
func CompareBaseline(previousSamples, currentSamples []int64) Comparison {
	c := Comparison{
		PreviousMedianMS: MedianInt64(previousSamples),
		CurrentMedianMS:  MedianInt64(currentSamples),
	}
	if c.PreviousMedianMS <= 0 {
		c.Verdict = VerdictNoBaseline
		return c
	}
	if c.CurrentMedianMS <= 0 {
		c.Verdict = VerdictRegressed
		c.DeltaPercent = math.Inf(1)
		return c
	}
	c.DeltaPercent = (c.CurrentMedianMS - c.PreviousMedianMS) / c.PreviousMedianMS * 100
	switch {
	case math.Abs(c.DeltaPercent) < RegressionThresholdPercent:
		c.Verdict = VerdictUnchanged
	case c.DeltaPercent < 0:
		c.Verdict = VerdictImproved
	default:
		c.Verdict = VerdictRegressed
	}
	return c
}

// WorkloadExecutor runs one workload on the media boundary (the Rust
// renderer). The suite times the execution wall clock itself; the executor
// only reports success/failure.
type WorkloadExecutor interface {
	RunWorkload(ctx context.Context, workload Workload) error
}

// BaselineSource returns the recorded wall-time samples (one per past run)
// for a workload, oldest first.
type BaselineSource interface {
	WorkloadSamples(ctx context.Context, workloadID string) ([]int64, error)
}

// Suite runs the fixed workloads and compares each against its baseline.
type Suite struct {
	executor           WorkloadExecutor
	baselines          BaselineSource
	registry           Registry
	samplesPerWorkload int
	now                func() time.Time
}

// NewBenchmarkSuite builds the suite. Fail-open on registry (nil disables
// persistence — comparisons still return); the executor and baselines are
// required (a nil executor is a hard error at Run).
func NewBenchmarkSuite(executor WorkloadExecutor, baselines BaselineSource, registry Registry, samplesPerWorkload int) *Suite {
	if samplesPerWorkload <= 0 {
		samplesPerWorkload = 5
	}
	return &Suite{
		executor:           executor,
		baselines:          baselines,
		registry:           registry,
		samplesPerWorkload: samplesPerWorkload,
		now:                time.Now,
	}
}

// SetClock overrides the suite clock (tests).
func (s *Suite) SetClock(now func() time.Time) {
	if s != nil && now != nil {
		s.now = now
	}
}

// Run executes every workload samplesPerWorkload times, records the median
// as a performance_run (when a registry is wired), and returns one
// Comparison per workload. An executor failure during sampling records the
// completed samples and continues with the next workload.
func (s *Suite) Run(ctx context.Context, workloads []Workload) ([]Comparison, error) {
	if s == nil || s.executor == nil {
		return nil, fmt.Errorf("performance benchmark suite: executor is required")
	}
	comparisons := make([]Comparison, 0, len(workloads))
	for _, workload := range workloads {
		comparison, err := s.runWorkload(ctx, workload)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, comparison)
	}
	return comparisons, nil
}

func (s *Suite) runWorkload(ctx context.Context, workload Workload) (Comparison, error) {
	var samples []int64
	for i := 0; i < s.samplesPerWorkload; i++ {
		started := time.Now()
		if err := s.executor.RunWorkload(ctx, workload); err != nil {
			return Comparison{}, fmt.Errorf("performance benchmark workload %s sample %d: %w", workload.WorkloadID, i+1, err)
		}
		samples = append(samples, time.Since(started).Milliseconds())
	}
	var previous []int64
	if s.baselines != nil {
		var err error
		previous, err = s.baselines.WorkloadSamples(ctx, workload.WorkloadID)
		if err != nil {
			return Comparison{}, fmt.Errorf("performance benchmark baseline %s: %w", workload.WorkloadID, err)
		}
	}
	comparison := CompareBaseline(previous, samples)
	comparison.WorkloadID = workload.WorkloadID
	if s.registry != nil {
		median := MedianInt64(samples)
		now := s.now().UTC()
		if err := s.registry.RecordRun(ctx, Run{
			RunID:           benchmarkRunID(workload.WorkloadID, now),
			JobID:           "",
			WorkloadID:      workload.WorkloadID,
			WorkloadVersion: workload.Version,
			Status:          "SUCCEEDED",
			WallMS:          int64(median),
			StartedAt:       formatPerformanceTime(now),
			CompletedAt:     formatPerformanceTime(s.now().UTC()),
		}); err != nil {
			return Comparison{}, fmt.Errorf("performance benchmark record run %s: %w", workload.WorkloadID, err)
		}
	}
	return comparison, nil
}

func benchmarkRunID(workloadID string, now time.Time) string {
	return fmt.Sprintf("%s-%s", workloadID, now.UTC().Format(time.RFC3339Nano))
}

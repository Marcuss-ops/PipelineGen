package scriptgeneration

// runner_phase_overlay_render_metrics_test.go pins the PROMETHEUS projection of
// the overlay render lane.
//
// The observability gap this closes: the run report (performance_operations)
// already knows the overlay_render stage wall time, but nothing exported the
// render lane to Prometheus — so there was no dashboard, no alerting and no
// way to compare the Chronon GPU render/encode split across runs. These tests
// assert the boundary records what it measured, that the failure path is
// distinguishable from success, and that the duplicated first-item artifact is
// never counted twice.
//
// Every assertion is a DELTA: the collectors are process-global (promauto), so
// a test that pinned absolute values would break the moment another test in the
// package exercised the same boundary.

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// chrononVulkanBackend is the backend literal the CERTIFIED artifacts actually
// carry. It was read from a live Mike Tyson render artifact, not guessed: the
// wire value is the rendering contract's own vocabulary, so it is pinned here
// and a rename in the render lane must surface as a failure in this file.
const chrononVulkanBackend = "vulkan"

// gpuTelemetryRenderEnqueuer returns a render reference whose artifacts carry
// the owner-measured Chronon telemetry, with the per-item lineage populated.
type gpuTelemetryRenderEnqueuer struct {
	items []OverlayItemRenderReference
}

func (e *gpuTelemetryRenderEnqueuer) EnqueueChrononPlan(_ context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	ref := RenderReference{JobID: plan.PlanID, Status: "COMPLETED", Items: e.items}
	// The real wire duplicates the FIRST item's artifact onto Artifact for the
	// legacy document consumers — the shape that would double-count.
	if len(e.items) > 0 {
		ref.Artifact = e.items[0].Artifact
	}
	return ref, nil
}

// failingRenderEnqueuer stands in for a render that fails remotely.
type failingRenderEnqueuer struct{ err error }

func (e *failingRenderEnqueuer) EnqueueChrononPlan(context.Context, capabilityoverlay.OverlayPlan) (RenderReference, error) {
	return RenderReference{}, e.err
}

// metricValue reads a single-series counter/gauge.
func metricValue(t *testing.T, collector prometheus.Collector) float64 {
	t.Helper()
	return testutil.ToFloat64(collector)
}

// histogramSampleTotal sums the observation count of every series of a
// histogram collector. testutil.ToFloat64 cannot read a histogram (it emits
// sample_count and sample_sum as separate metrics), so the test gathers the
// collector through a private registry instead.
func histogramSampleTotal(t *testing.T, collector prometheus.Collector) uint64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(collector))
	families, err := registry.Gather()
	require.NoError(t, err)
	var total uint64
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			total += metric.GetHistogram().GetSampleCount()
		}
	}
	return total
}

func TestOverlayRenderPhaseRecordsBoundaryAndGpuMetrics(t *testing.T) {
	itemArtifact := func(id string, renderMS, encodeMS int64, frames int) OverlayItemRenderReference {
		return OverlayItemRenderReference{
			ItemID: id, JobID: "render-" + id, Status: "COMPLETED",
			Artifact: &RenderArtifact{
				ID: "artifact-" + id, Kind: "overlay", Backend: chrononVulkanBackend,
				RenderMS: renderMS, EncodeMS: encodeMS, FrameCount: frames,
			},
		}
	}
	enqueuer := &gpuTelemetryRenderEnqueuer{items: []OverlayItemRenderReference{
		itemArtifact("overlay-scene-0-tom-hanks", 1200, 240, 120),
		itemArtifact("overlay-scene-0-los-angeles", 800, 160, 96),
	}}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-metrics", VideoID: "video-metrics",
			Items: []capabilityoverlay.OverlayItem{{ID: "a"}, {ID: "b"}},
		},
	}

	successBefore := metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess))
	failureBefore := metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure))
	boundarySamplesBefore := histogramSampleTotal(t, observability.OverlayRenderDurationSeconds)
	itemsSamplesBefore := histogramSampleTotal(t, observability.OverlayRenderItems)
	renderSamplesBefore := histogramSampleTotal(t, observability.ChrononRenderSeconds)
	encodeSamplesBefore := histogramSampleTotal(t, observability.ChrononEncodeSeconds)
	framesBefore := metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend))

	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-metrics", req, ExecutionContext{}, 0, audioCompileState{}, result))
	require.NotNil(t, result.OverlayRender)

	require.Equal(t, successBefore+1, metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess)))
	require.Equal(t, failureBefore, metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure)),
		"a successful render must never touch the failure series")
	require.Equal(t, boundarySamplesBefore+1, histogramSampleTotal(t, observability.OverlayRenderDurationSeconds),
		"the blocking boundary must record exactly one wall-time observation")
	require.Equal(t, itemsSamplesBefore+1, histogramSampleTotal(t, observability.OverlayRenderItems),
		"the semantic plan size must be recorded once per render")
	require.Equal(t, renderSamplesBefore+2, histogramSampleTotal(t, observability.ChrononRenderSeconds),
		"each per-item artifact contributes its own Chronon render phase")
	require.Equal(t, encodeSamplesBefore+2, histogramSampleTotal(t, observability.ChrononEncodeSeconds))
	require.Equal(t, framesBefore+216, metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend)),
		"frames are summed from the item lineage, never from the duplicated first-item artifact")
}

func TestOverlayRenderPhaseFailureRecordsOutcomeWithoutGpuMetrics(t *testing.T) {
	repo := newInMemRunRepository()
	req := defaultTestRequest()
	req.Render.Enabled = true
	runID := "run-metrics-failure"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	renderErr := errors.New("render job failed with exit code 1")
	runner := &Runner{
		overlayRenderEnqueuer: &failingRenderEnqueuer{err: renderErr},
		log:                   zap.NewNop(),
		recorder:              noopExecutionRecorder{},
		repo:                  repo,
	}
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-failure", VideoID: "video-failure",
			Items: []capabilityoverlay.OverlayItem{{ID: "a"}},
		},
	}

	successBefore := metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess))
	failureBefore := metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure))
	boundarySamplesBefore := histogramSampleTotal(t, observability.OverlayRenderDurationSeconds)
	renderSamplesBefore := histogramSampleTotal(t, observability.ChrononRenderSeconds)
	framesBefore := metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend))

	require.False(t, runner.runOverlayRenderPhase(context.Background(), runID, req, ExecutionContext{}, 0, audioCompileState{}, result),
		"a failed render must fail the phase")
	require.Nil(t, result.OverlayRender, "a failed render must not persist a render reference")

	require.Equal(t, failureBefore+1, metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeFailure)))
	require.Equal(t, successBefore, metricValue(t, observability.OverlayRenderTotal.WithLabelValues(renderOutcomeSuccess)))
	require.Equal(t, boundarySamplesBefore+1, histogramSampleTotal(t, observability.OverlayRenderDurationSeconds),
		"the wall time of a FAILED render is still a measured wait")
	require.Equal(t, renderSamplesBefore, histogramSampleTotal(t, observability.ChrononRenderSeconds),
		"a failed render certified no artifact, so the GPU series must stay untouched")
	require.Equal(t, framesBefore, metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend)))
}

// TestRecordChrononArtifactMetricsPrefersItemLineageOverDuplicatedArtifact pins
// the double-count guard directly: the wire duplicates the first item's
// artifact onto RenderReference.Artifact, so an implementation that observed
// both would report 2 frames for a single-render run.
func TestRecordChrononArtifactMetricsPrefersItemLineageOverDuplicatedArtifact(t *testing.T) {
	first := &RenderArtifact{ID: "artifact-0", Backend: chrononVulkanBackend, RenderMS: 500, EncodeMS: 100, FrameCount: 50}
	second := &RenderArtifact{ID: "artifact-1", Backend: chrononVulkanBackend, RenderMS: 700, EncodeMS: 140, FrameCount: 70}
	ref := RenderReference{
		JobID:    "job-dup",
		Status:   "COMPLETED",
		Artifact: first, // the wire duplicate of items[0]
		Items: []OverlayItemRenderReference{
			{ItemID: "item-0", Artifact: first},
			{ItemID: "item-1", Artifact: second},
		},
	}

	require.Equal(t, []*RenderArtifact{first, second}, renderedArtifacts(ref))

	framesBefore := metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend))
	renderSamplesBefore := histogramSampleTotal(t, observability.ChrononRenderSeconds)

	recordChrononArtifactMetrics(ref)

	require.Equal(t, framesBefore+120, metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(chrononVulkanBackend)),
		"50 + 70 frames — the duplicated first-item artifact must not add another 50")
	require.Equal(t, renderSamplesBefore+2, histogramSampleTotal(t, observability.ChrononRenderSeconds))

	// A worker that reported no phase must not be observed at all: a zero would
	// drag the histogram down and hide the missing measurement.
	silentBefore := histogramSampleTotal(t, observability.ChrononRenderSeconds)
	recordChrononArtifactMetrics(RenderReference{Artifact: &RenderArtifact{ID: "no-telemetry", Backend: chrononVulkanBackend}})
	require.Equal(t, silentBefore, histogramSampleTotal(t, observability.ChrononRenderSeconds))
}

// TestRecordChrononArtifactMetricsLabelsUnknownBackend pins that the backend
// label is never the empty string: an unreported backend must not create a
// nameless series that dashboards silently drop.
func TestRecordChrononArtifactMetricsLabelsUnknownBackend(t *testing.T) {
	framesBefore := metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(unknownChrononBackend))
	recordChrononArtifactMetrics(RenderReference{Artifact: &RenderArtifact{ID: "no-backend", FrameCount: 12}})
	require.Equal(t, framesBefore+12, metricValue(t, observability.ChrononFramesRenderedTotal.WithLabelValues(unknownChrononBackend)))
}

// TestOverlayRenderPhaseRecordsPlanSizeEvenWithoutEnqueueContract keeps the
// skip contract honest: a run that does not render must not observe anything.
func TestOverlayRenderPhaseSkipRecordsNothing(t *testing.T) {
	runner := &Runner{log: zap.NewNop()}
	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-skip", VideoID: "video-skip",
			Items: []capabilityoverlay.OverlayItem{{ID: "a"}},
		},
	}
	itemsSamplesBefore := histogramSampleTotal(t, observability.OverlayRenderItems)
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-skip", req, ExecutionContext{}, 0, audioCompileState{}, result))
	require.Equal(t, itemsSamplesBefore, histogramSampleTotal(t, observability.OverlayRenderItems),
		"an unwired enqueuer renders nothing, so nothing may be measured")
}

type languageCapturingRenderEnqueuer struct {
	languages []string
}

func (e *languageCapturingRenderEnqueuer) EnqueueChrononPlan(_ context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.languages = append(e.languages, plan.Language)
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED"}, nil
}

func TestOverlayRenderPhaseRendersEveryLocalizedPlanInRequestOrder(t *testing.T) {
	enqueuer := &languageCapturingRenderEnqueuer{}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "run-en", VideoID: "video-en", Language: "en",
			Items: []capabilityoverlay.OverlayItem{{ID: "phrase-en"}},
		},
		LocalizedOverlayPlans: map[Language]*capabilityoverlay.OverlayPlan{
			"it": {SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "run-it", VideoID: "video-it", Language: "it", Items: []capabilityoverlay.OverlayItem{{ID: "phrase-it"}}},
			"fr": {SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "run-fr", VideoID: "video-fr", Language: "fr", Items: []capabilityoverlay.OverlayItem{{ID: "phrase-fr"}}},
		},
	}
	req := defaultTestRequest()
	req.SourceLanguage = "en"
	req.Languages = []Language{"it", "en", "fr"}
	req.Render.Enabled = true

	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-multilingual", req, ExecutionContext{}, 0, audioCompileState{}, result))
	require.Equal(t, []string{"en", "it", "fr"}, enqueuer.languages,
		"the source plan renders first, then localized plans follow caller language order")
	require.Equal(t, "run-en", result.OverlayRender.JobID)
	require.Equal(t, "run-it", result.LocalizedOverlayRenders["it"].JobID)
	require.Equal(t, "run-fr", result.LocalizedOverlayRenders["fr"].JobID)

	// A recovered run with the completed references already in its result must
	// reuse them rather than submit duplicate GPU work.
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-multilingual", req, ExecutionContext{}, 0, audioCompileState{}, result))
	require.Len(t, enqueuer.languages, 3)
}

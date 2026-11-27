package cliprender

// aliases_subpackages.go keeps the cliprender API source-compatible after the
// telemetry projection (V2 metrics report, Chronon sidecar) and the batch
// parent aggregator moved into leaf subpackages. The capability root was over
// its package line budget; the split is a location change, not an API change.
//
// Type aliases carry the methods, so callers keep writing cliprender.Metric,
// cliprender.RenderMetricsV2, cliprender.ChrononSidecar and
// cliprender.ParentAggregator exactly as before. The var aliases carry the
// constructors; they are variables rather than wrappers so no second
// implementation can drift from the subpackage.

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/telemetry"
)

type (
	// Metric is one instrumented V2 report value (see telemetry.Metric).
	Metric = telemetry.Metric
	// RenderMetricsV2 is the canonical clip.render execution report.
	RenderMetricsV2 = telemetry.RenderMetricsV2
	// ChrononSidecar is the typed Chronon *.timing.json projection.
	ChrononSidecar = telemetry.ChrononSidecar
	// ChrononMetricsAdapter projects sidecar phases into the registry.
	ChrononMetricsAdapter = telemetry.ChrononMetricsAdapter
	// ChrononMetricsPublishOptions carries the attempt facts the sidecar omits.
	ChrononMetricsPublishOptions = telemetry.ChrononMetricsPublishOptions
	// ParentAggregator aggregates batch children into their parent job.
	ParentAggregator = batch.ParentAggregator
	// AggregatorJobsService is the job port the aggregator consumes.
	AggregatorJobsService = batch.AggregatorJobsService
)

const (
	// NotInstrumented is the V2 sentinel for a phase with no instrumentation.
	NotInstrumented = telemetry.NotInstrumented

	// Canonical clip.render stages, owned by telemetry/stages.go.
	StageClipDestinationResolve = telemetry.StageClipDestinationResolve
	StageClipPrepare            = telemetry.StageClipPrepare
	StageClipSubtitles          = telemetry.StageClipSubtitles
	StageClipRender             = telemetry.StageClipRender
	StageClipProbe              = telemetry.StageClipProbe
	StageClipPublish            = telemetry.StageClipPublish

	// DefaultParentAggregationInterval is the clip.render parent RECOVERY
	// cadence owned by the batch package.
	DefaultParentAggregationInterval = batch.DefaultParentAggregationInterval
	// ParentStateWaitingChildren is the parent_state the submit phase writes.
	ParentStateWaitingChildren = batch.ParentStateWaitingChildren

	// Chronon performance-registry operation names.
	ChrononOperationStartup      = telemetry.ChrononOperationStartup
	ChrononOperationInputOpen    = telemetry.ChrononOperationInputOpen
	ChrononOperationPrepare      = telemetry.ChrononOperationPrepare
	ChrononOperationRenderLoop   = telemetry.ChrononOperationRenderLoop
	ChrononOperationEncoderDrain = telemetry.ChrononOperationEncoderDrain
	ChrononOperationFFprobe      = telemetry.ChrononOperationFFprobe
	ChrononOperationSHA256       = telemetry.ChrononOperationSHA256
)

var (
	// NewRenderMetricsV2 constructs a fully NOT_INSTRUMENTED report.
	NewRenderMetricsV2 = telemetry.NewRenderMetricsV2
	// ParseChrononSidecar decodes a Chronon timing document.
	ParseChrononSidecar = telemetry.ParseChrononSidecar
	// NewChrononMetricsAdapter constructs the phase projection adapter.
	NewChrononMetricsAdapter = telemetry.NewChrononMetricsAdapter
	// NewParentAggregator constructs the batch parent aggregator.
	NewParentAggregator = batch.NewParentAggregator
)

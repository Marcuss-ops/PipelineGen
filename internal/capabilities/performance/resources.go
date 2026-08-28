package performance

import (
	"context"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ResourceObservation is one raw host/process resource sample. Nil fields mean
// that the platform could not observe that resource; missing measurements are
// never represented as zero values.
//
// Delta-derived fields (CPUAvgPct, DiskReadBytes, ...) carry the value
// measured over the interval since the previous sample. Peak fields
// (CPUPeakPct, RSSPeakBytes, GPUPeakPct, VRAMPeakBytes, *_PeakC) carry the
// run-level rolling peak as of this sample, so a reader that takes MAX()
// across the run's rows obtains the true peak. Throttled reports the
// throttling state at this sample; MAX() across rows answers "was the run
// ever throttled".
type ResourceObservation struct {
	ObservationID string
	RunID         string
	JobID         string
	AttemptID     string
	WorkerID      string
	Host          string
	ObservedAt    string

	CPUAvgPct        *float64
	CPUPeakPct       *float64
	RSSAvgBytes      *int64
	RSSPeakBytes     *int64
	SwapInBytes      *int64
	SwapOutBytes     *int64
	DiskReadBytes    *int64
	DiskWriteBytes   *int64
	DiskUtilPct      *float64
	IOWaitPct        *float64
	DiskQueueDepth   *float64
	GPUAvgPct        *float64
	GPUPeakPct       *float64
	VRAMPeakBytes    *int64
	EncoderAvgPct    *float64
	DecoderAvgPct    *float64
	CPUTempPeakC     *float64
	GPUTempPeakC     *float64
	TemperaturePeakC *float64 // legacy combined peak (max of CPU/GPU)
	Throttled        *bool
	NetworkRXBytes   *int64
	NetworkTXBytes   *int64
	MetadataJSON     string
}

// ResourceSampler obtains canonical samples and persists them through the
// supplied sink. Implementations must not write ad-hoc benchmark files.
type ResourceSampler interface {
	Sample(context.Context, SampleIdentity) (ResourceObservation, error)
}

// RunSampler is the run-scoped sampling port consumed by the worker; it is
// an alias of the canonical kernel port (internal/kernel/observability) so
// kernel, capabilities and platform all share one contract.
type RunSampler = kernobs.RunResourceSampler

// SampleIdentity is the canonical resource sample identity; alias of the
// kernel type.
type SampleIdentity = kernobs.ResourceSampleIdentity

type ResourceObservationStore interface {
	RecordResourceObservation(context.Context, ResourceObservation) error
}

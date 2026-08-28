package performance

import "context"

// ResourceObservation is one raw host/process resource sample. Nil fields mean
// that the platform could not observe that resource; missing measurements are
// never represented as zero values.
type ResourceObservation struct {
	ObservationID string
	RunID         string
	JobID         string
	WorkerID      string
	Host          string
	ObservedAt    string

	CPUAvgPct        *float64
	CPUPeakPct       *float64
	RSSAvgBytes      *int64
	RSSPeakBytes     *int64
	GPUAvgPct        *float64
	GPUPeakPct       *float64
	VRAMPeakBytes    *int64
	EncoderAvgPct    *float64
	TemperaturePeakC *float64
	DiskReadBytes    *int64
	DiskWriteBytes   *int64
	NetworkRXBytes   *int64
	NetworkTXBytes   *int64
	MetadataJSON     string
}

// ResourceSampler obtains canonical samples and persists them through the
// supplied sink. Implementations must not write ad-hoc benchmark files.
type ResourceSampler interface {
	Sample(context.Context, SampleIdentity) (ResourceObservation, error)
}

type SampleIdentity struct {
	RunID, JobID, WorkerID, Host string
}

type ResourceObservationStore interface {
	RecordResourceObservation(context.Context, ResourceObservation) error
}

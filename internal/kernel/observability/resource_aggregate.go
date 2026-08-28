package observability

import "time"

// ResourceAggregate is a compact long-retention projection derived from raw
// samples. It contains no second timing source: interval boundaries come from
// the minimum/maximum ResourceSample. Unsupported facts remain absent.
type ResourceAggregate struct {
	SchemaVersion   int       `json:"schema_version"`
	SampleCount     int       `json:"sample_count"`
	FirstObservedAt time.Time `json:"first_observed_at,omitempty"`
	LastObservedAt  time.Time `json:"last_observed_at,omitempty"`
	CPUAvgPct       *float64  `json:"cpu_avg_pct,omitempty"`
	CPUPeakPct      *float64  `json:"cpu_peak_pct,omitempty"`
	RSSPeakBytes    *int64    `json:"rss_peak_bytes,omitempty"`
	GPUAvgPct       *float64  `json:"gpu_avg_pct,omitempty"`
	GPUPeakPct      *float64  `json:"gpu_peak_pct,omitempty"`
	VRAMPeakBytes   *int64    `json:"vram_peak_bytes,omitempty"`
	CPUTempPeakC    *float64  `json:"cpu_temperature_peak_c,omitempty"`
	GPUTempPeakC    *float64  `json:"gpu_temperature_peak_c,omitempty"`
	UploadBytes     *int64    `json:"upload_bytes,omitempty"`
	DiskReadBytes   *int64    `json:"disk_read_bytes,omitempty"`
	DiskWriteBytes  *int64    `json:"disk_write_bytes,omitempty"`
}

func AggregateResourceSamples(samples []ResourceSample) ResourceAggregate {
	a := ResourceAggregate{SchemaVersion: RunResourceReportSchemaVersion, SampleCount: len(samples)}
	var cpu, gpu []float64
	for _, s := range samples {
		if a.FirstObservedAt.IsZero() || s.ObservedAt.Before(a.FirstObservedAt) {
			a.FirstObservedAt = s.ObservedAt
		}
		if a.LastObservedAt.IsZero() || s.ObservedAt.After(a.LastObservedAt) {
			a.LastObservedAt = s.ObservedAt
		}
		if s.CPUAvgPct != nil {
			cpu = append(cpu, *s.CPUAvgPct)
		}
		if s.GPUUtilPct != nil {
			gpu = append(gpu, *s.GPUUtilPct)
		}
		a.CPUPeakPct = maxFPtr(a.CPUPeakPct, s.CPUPeakPct, s.CPUAvgPct)
		a.GPUPeakPct = maxFPtr(a.GPUPeakPct, s.GPUUtilPct)
		a.RSSPeakBytes = maxIPtr(a.RSSPeakBytes, s.RSSPeakBytes, s.RSSAvgBytes)
		a.VRAMPeakBytes = maxIPtr(a.VRAMPeakBytes, s.VRAMBytes)
		a.CPUTempPeakC = maxFPtr(a.CPUTempPeakC, s.CPUTemperatureC)
		a.GPUTempPeakC = maxFPtr(a.GPUTempPeakC, s.GPUTemperatureC)
		a.UploadBytes = maxIPtr(a.UploadBytes, s.UploadBytes)
		a.DiskReadBytes = maxIPtr(a.DiskReadBytes, s.DiskReadBytes)
		a.DiskWriteBytes = maxIPtr(a.DiskWriteBytes, s.DiskWriteBytes)
	}
	a.CPUAvgPct = meanPtr(cpu)
	a.GPUAvgPct = meanPtr(gpu)
	return a
}

func meanPtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	out := sum / float64(len(values))
	return &out
}
func maxFPtr(current *float64, values ...*float64) *float64 {
	out := current
	for _, v := range values {
		if v != nil && (out == nil || *v > *out) {
			x := *v
			out = &x
		}
	}
	return out
}
func maxIPtr(current *int64, values ...*int64) *int64 {
	out := current
	for _, v := range values {
		if v != nil && (out == nil || *v > *out) {
			x := *v
			out = &x
		}
	}
	return out
}

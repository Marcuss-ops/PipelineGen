package observability

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// RunResourceReportSchemaVersion is the wire/storage version of
// RunResourceReport. Additive changes require a new version; readers must
// continue accepting older versions.
const RunResourceReportSchemaVersion = 1

// RunResourceReport is the canonical resource-observability envelope for one
// run/attempt. It contains raw facts only; avg/p95/peak values are derived by
// readers from Samples and are never written by samplers as a second source of
// truth.
type RunResourceReport struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	JobID         string           `json:"job_id"`
	AttemptID     string           `json:"attempt_id"`
	WorkerID      string           `json:"worker_id,omitempty"`
	Host          string           `json:"host,omitempty"`
	StartedAt     time.Time        `json:"started_at,omitempty"`
	FinishedAt    time.Time        `json:"finished_at,omitempty"`
	Samples       []ResourceSample `json:"samples,omitempty"`
}

// ResourceSample is one timestamped measurement fact. Nil values mean the
// platform could not observe that resource; zero is a real observed value.
// Byte counters are cumulative process/host counters at sample time.
type ResourceSample struct {
	SampleID   string    `json:"sample_id"`
	ObservedAt time.Time `json:"observed_at"`

	CPUAvgPct        *float64 `json:"cpu_avg_pct,omitempty"`
	CPUP95Pct        *float64 `json:"cpu_p95_pct,omitempty"`
	CPUPeakPct       *float64 `json:"cpu_peak_pct,omitempty"`
	Load1            *float64 `json:"load1,omitempty"`
	Load5            *float64 `json:"load5,omitempty"`
	FFmpegCPUAvgPct  *float64 `json:"ffmpeg_cpu_avg_pct,omitempty"`
	FFmpegCPUPeakPct *float64 `json:"ffmpeg_cpu_peak_pct,omitempty"`
	CPUUserMS        *int64   `json:"cpu_user_ms,omitempty"`
	CPUSystemMS      *int64   `json:"cpu_system_ms,omitempty"`
	FFmpegThreads    *int64   `json:"ffmpeg_threads,omitempty"`
	RenderThreads    *int64   `json:"total_render_threads,omitempty"`
	ContextSwitches  *int64   `json:"context_switches,omitempty"`

	RSSAvgBytes          *int64 `json:"rss_avg_bytes,omitempty"`
	RSSPeakBytes         *int64 `json:"rss_peak_bytes,omitempty"`
	SystemAvailableBytes *int64 `json:"system_available_bytes,omitempty"`
	SwapInBytes          *int64 `json:"swap_in_bytes,omitempty"`
	SwapOutBytes         *int64 `json:"swap_out_bytes,omitempty"`

	DiskReadBytes  *int64   `json:"disk_read_bytes,omitempty"`
	DiskWriteBytes *int64   `json:"disk_write_bytes,omitempty"`
	ReadMBPS       *float64 `json:"read_mb_s,omitempty"`
	WriteMBPS      *float64 `json:"write_mb_s,omitempty"`
	IOWaitPct      *float64 `json:"io_wait_pct,omitempty"`
	DiskUtilPct    *float64 `json:"disk_util_pct,omitempty"`
	QueueDepth     *int64   `json:"queue_depth,omitempty"`

	ScratchFreeBytes    *int64 `json:"scratch_free_bytes,omitempty"`
	ScratchBytesWritten *int64 `json:"scratch_bytes_written,omitempty"`
	ScratchFilesystem   string `json:"scratch_filesystem,omitempty"`
	ScratchDevice       string `json:"scratch_device,omitempty"`

	UploadBytes    *int64   `json:"upload_bytes,omitempty"`
	UploadWallMS   *int64   `json:"upload_wall_ms,omitempty"`
	UploadMBPS     *float64 `json:"upload_mbps,omitempty"`
	HTTPRTTMS      *int64   `json:"http_rtt_ms,omitempty"`
	NetworkRXBytes *int64   `json:"network_rx_bytes,omitempty"`
	NetworkTXBytes *int64   `json:"network_tx_bytes,omitempty"`

	GPUUtilPct      *float64 `json:"gpu_util_pct,omitempty"`
	VRAMBytes       *int64   `json:"vram_bytes,omitempty"`
	EncoderUtilPct  *float64 `json:"encoder_util_pct,omitempty"`
	DecoderUtilPct  *float64 `json:"decoder_util_pct,omitempty"`
	PCIeRXBytes     *int64   `json:"pcie_rx_bytes,omitempty"`
	PCIeTXBytes     *int64   `json:"pcie_tx_bytes,omitempty"`
	CPUTemperatureC *float64 `json:"cpu_temperature_c,omitempty"`
	GPUTemperatureC *float64 `json:"gpu_temperature_c,omitempty"`
	Throttling      *bool    `json:"throttling,omitempty"`
	GPUFrequencyMHz *float64 `json:"gpu_frequency_mhz,omitempty"`
}

// Validate checks identity, schema version, timestamps and sample IDs before
// persistence. Unsupported resources remain valid when represented as nil.
func (r RunResourceReport) Validate() error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = RunResourceReportSchemaVersion
	}
	if r.SchemaVersion != RunResourceReportSchemaVersion {
		return errors.New("observability: unsupported RunResourceReport schema version")
	}
	if strings.TrimSpace(r.RunID) == "" || strings.TrimSpace(r.JobID) == "" || strings.TrimSpace(r.AttemptID) == "" {
		return errors.New("observability: run_id, job_id and attempt_id are required")
	}
	if !r.StartedAt.IsZero() && !r.FinishedAt.IsZero() && r.FinishedAt.Before(r.StartedAt) {
		return errors.New("observability: resource report finished_at precedes started_at")
	}
	seen := make(map[string]struct{}, len(r.Samples))
	for _, sample := range r.Samples {
		if strings.TrimSpace(sample.SampleID) == "" {
			return errors.New("observability: resource sample_id is required")
		}
		if _, ok := seen[sample.SampleID]; ok {
			return errors.New("observability: duplicate resource sample_id")
		}
		seen[sample.SampleID] = struct{}{}
		if sample.ObservedAt.IsZero() {
			return errors.New("observability: resource observed_at is required")
		}
	}
	return nil
}

// MarshalJSON applies the current schema version when callers construct the
// report with the zero value for convenience.
func (r RunResourceReport) MarshalJSON() ([]byte, error) {
	type alias RunResourceReport
	if r.SchemaVersion == 0 {
		r.SchemaVersion = RunResourceReportSchemaVersion
	}
	return json.Marshal(alias(r))
}

// UnmarshalJSON rejects unknown future versions but accepts version 0 as the
// pre-versioned representation for backward compatibility.
func (r *RunResourceReport) UnmarshalJSON(data []byte) error {
	type alias RunResourceReport
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.SchemaVersion == 0 {
		decoded.SchemaVersion = RunResourceReportSchemaVersion
	}
	if decoded.SchemaVersion != RunResourceReportSchemaVersion {
		return errors.New("observability: unsupported RunResourceReport schema version")
	}
	*r = RunResourceReport(decoded)
	return nil
}

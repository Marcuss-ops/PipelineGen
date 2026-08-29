package cliprender

// chronon_sidecar.go owns the typed projection of Chronon's canonical
// *.timing.json sidecar (the frame-timing document the video pipe exporter
// writes next to the rendered output). Chronon owns these measurements;
// PipelineGen only transports them and never re-times or fabricates missing
// phases.
//
// The document carries the per-phase EXCLUSIVE wall timeline
// (exclusive_wall_timeline: startup/input_open/prepare/render_loop/
// encoder_drain/ffprobe/sha256), the GPU/backend context (job.gpu:
// effective_backend/decoder_backend/encoder_backend + CUDA byte counters),
// the asset-cache counters (cache) and the render summary (summary). The
// unbounded per-frame array (frame_times_ms) is deep-profiling detail and is
// deliberately not part of this projection.
//
// NO-FAKE-AVAILABILITY: a phase or counter the sidecar did not measure stays
// a nil pointer — never a fabricated zero. A measured 0.0 (e.g. an input that
// opened instantly) is a real measurement and is preserved as a non-nil
// pointer to 0.

import (
	"encoding/json"
	"fmt"
)

// ChrononSidecar is the typed projection of Chronon's frame-timing sidecar.
// Every phase/counter is a pointer so "absent" (nil) is distinguishable from
// "measured and zero" (non-nil) — the same honesty rule the V2 metrics report
// applies with its NOT_INSTRUMENTED sentinel.
type ChrononSidecar struct {
	// Exclusive wall timeline phases (exclusive_wall_timeline). The exporter
	// only lists phases it actually measured; absent phases stay nil.
	StartupMS              *float64
	InputOpenMS            *float64
	PrepareMS              *float64
	RenderLoopMS           *float64
	EncoderDrainFinalizeMS *float64
	FFprobeMS              *float64
	SHA256MS               *float64
	ProcessWallMS          *float64
	AccountedPercent       *float64

	// GPU/backend context (job.gpu). The strings are the engine's own
	// vocabulary ("direct_yuv_cuda"|"vulkan", "nvdec"|"software"|"hybrid",
	// "nvenc"|"software"); "unknown" is the exporter's sentinel for a path
	// that ran no GPU backend. Byte counters are nil when the software path
	// emitted null.
	Backend                 string
	Decoder                 string
	Encoder                 string
	GPUUploadBytes          *uint64
	GPUReadbackBytes        *uint64
	EncoderStagingCopyBytes *uint64

	// Asset-cache counters (cache). nil when the sidecar predates them.
	GPUAssetCacheHits   *uint64
	GPUAssetCacheMisses *uint64

	// Render summary projection (summary). nil when the sidecar predates it.
	EndToEndFPS          *float64
	RenderLoopFPS        *float64
	RealtimeFactor       *float64
	GraphReusedFrames    *uint64
	FastPathReusedFrames *uint64
}

// chrononSidecarDoc is the wire shape of the frame-timing document. Pointer
// fields keep "absent" (nil) distinct from "measured 0" (non-nil), which the
// exporter itself expresses with null values for not-measured fields.
type chrononSidecarDoc struct {
	Cache struct {
		GPUAssetCacheHits   *uint64 `json:"gpu_asset_cache_hits"`
		GPUAssetCacheMisses *uint64 `json:"gpu_asset_cache_misses"`
	} `json:"cache"`
	ExclusiveWallTimeline struct {
		ProcessWallMS          *float64 `json:"process_wall_ms"`
		StartupMS              *float64 `json:"startup_ms"`
		InputOpenMS            *float64 `json:"input_open_ms"`
		PrepareMS              *float64 `json:"prepare_ms"`
		RenderLoopMS           *float64 `json:"render_loop_ms"`
		EncoderDrainFinalizeMS *float64 `json:"encoder_drain_finalize_ms"`
		FFprobeMS              *float64 `json:"ffprobe_ms"`
		SHA256MS               *float64 `json:"sha256_ms"`
		AccountedPercent       *float64 `json:"accounted_percent"`
	} `json:"exclusive_wall_timeline"`
	Job struct {
		GPU struct {
			EffectiveBackend        *string `json:"effective_backend"`
			DecoderBackend          *string `json:"decoder_backend"`
			EncoderBackend          *string `json:"encoder_backend"`
			GPUUploadBytes          *uint64 `json:"gpu_upload_bytes"`
			GPUReadbackBytes        *uint64 `json:"gpu_readback_bytes"`
			EncoderStagingCopyBytes *uint64 `json:"encoder_staging_copy_bytes"`
		} `json:"gpu"`
	} `json:"job"`
	Summary struct {
		EndToEndFPS          *float64 `json:"end_to_end_fps"`
		RenderLoopFPS        *float64 `json:"render_loop_fps"`
		RealtimeFactor       *float64 `json:"realtime_factor"`
		GraphReusedFrames    *uint64  `json:"graph_reused_frames"`
		FastPathReusedFrames *uint64  `json:"fast_path_reused_frames"`
	} `json:"summary"`
}

// ParseChrononSidecar decodes a Chronon *.timing.json sidecar document into
// the typed projection. The schema has evolved across Chronon versions, so
// every section is optional: a document that predates exclusive_wall_timeline
// (or a job.gpu block, or the summary) parses without error and simply leaves
// the corresponding fields nil. Only a structurally invalid document errors.
func ParseChrononSidecar(data []byte) (*ChrononSidecar, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("chronon timing sidecar: empty document")
	}
	var doc chrononSidecarDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: decode: %w", err)
	}
	out := &ChrononSidecar{
		StartupMS:               doc.ExclusiveWallTimeline.StartupMS,
		InputOpenMS:             doc.ExclusiveWallTimeline.InputOpenMS,
		PrepareMS:               doc.ExclusiveWallTimeline.PrepareMS,
		RenderLoopMS:            doc.ExclusiveWallTimeline.RenderLoopMS,
		EncoderDrainFinalizeMS:  doc.ExclusiveWallTimeline.EncoderDrainFinalizeMS,
		FFprobeMS:               doc.ExclusiveWallTimeline.FFprobeMS,
		SHA256MS:                doc.ExclusiveWallTimeline.SHA256MS,
		ProcessWallMS:           doc.ExclusiveWallTimeline.ProcessWallMS,
		AccountedPercent:        doc.ExclusiveWallTimeline.AccountedPercent,
		GPUUploadBytes:          doc.Job.GPU.GPUUploadBytes,
		GPUReadbackBytes:        doc.Job.GPU.GPUReadbackBytes,
		EncoderStagingCopyBytes: doc.Job.GPU.EncoderStagingCopyBytes,
		GPUAssetCacheHits:       doc.Cache.GPUAssetCacheHits,
		GPUAssetCacheMisses:     doc.Cache.GPUAssetCacheMisses,
		EndToEndFPS:             doc.Summary.EndToEndFPS,
		RenderLoopFPS:           doc.Summary.RenderLoopFPS,
		RealtimeFactor:          doc.Summary.RealtimeFactor,
		GraphReusedFrames:       doc.Summary.GraphReusedFrames,
		FastPathReusedFrames:    doc.Summary.FastPathReusedFrames,
	}
	if doc.Job.GPU.EffectiveBackend != nil {
		out.Backend = *doc.Job.GPU.EffectiveBackend
	}
	if doc.Job.GPU.DecoderBackend != nil {
		out.Decoder = *doc.Job.GPU.DecoderBackend
	}
	if doc.Job.GPU.EncoderBackend != nil {
		out.Encoder = *doc.Job.GPU.EncoderBackend
	}
	return out, nil
}

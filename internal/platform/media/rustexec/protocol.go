package rustexec

import (
	"encoding/json"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	capabilityrender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

const ProtocolVersion = "mediaexec.v1"

type Operation string

const (
	OperationHealth             Operation = "health"
	OperationProbe              Operation = "probe"
	OperationCutBatch           Operation = "cut_batch"
	OperationNormalize          Operation = "normalize"
	OperationCutCopy            Operation = "cut_copy"
	OperationCutAndNormalize    Operation = "cut_and_normalize"
	OperationWatermark          Operation = "watermark"
	OperationExtractFrame       Operation = "extract_frame"
	OperationGenerateProxy      Operation = "generate_proxy"
	OperationGenerateStoryboard Operation = "generate_storyboard"
	OperationRemuxHLS           Operation = "remux_hls"
	OperationTrim               Operation = "trim"
	OperationRenderStock        Operation = "render_stock"
	OperationRenderClip         Operation = "render_clip"
	OperationAdminRender        Operation = "admin_render"
	OperationMergeInputs        Operation = "merge_inputs"
	OperationRemoveSilence      Operation = "remove_silence"
	OperationRenderAudioPlan    Operation = "render_audio_plan"
	OperationMuxAudioCopy       Operation = "mux_audio_copy"
)

func (o Operation) String() string { return string(o) }

func (o Operation) valid() bool {
	switch o {
	case OperationHealth, OperationProbe, OperationCutBatch, OperationNormalize,
		OperationCutCopy, OperationCutAndNormalize, OperationWatermark,
		OperationExtractFrame, OperationGenerateProxy, OperationGenerateStoryboard,
		OperationRemuxHLS, OperationTrim, OperationRenderStock, OperationRenderClip,
		OperationAdminRender, OperationMergeInputs, OperationRemoveSilence,
		OperationRenderAudioPlan, OperationMuxAudioCopy:
		return true
	default:
		return false
	}
}

type request struct {
	Version          string    `json:"version"`
	Operation        Operation `json:"operation"`
	FFmpegPath       string    `json:"ffmpeg_path,omitempty"`
	SourcePath       string    `json:"source_path,omitempty"`
	OutputPath       string    `json:"output_path,omitempty"`
	TimestampSec     float64   `json:"timestamp_sec,omitempty"`
	StartSec         float64   `json:"start_sec,omitempty"`
	EndSec           float64   `json:"end_sec,omitempty"`
	IntervalFrames   uint32    `json:"interval_frames,omitempty"`
	Columns          uint32    `json:"columns,omitempty"`
	Rows             uint32    `json:"rows,omitempty"`
	Codec            string    `json:"codec,omitempty"`
	Preset           string    `json:"preset,omitempty"`
	CRF              int       `json:"crf,omitempty"`
	Width            uint32    `json:"width,omitempty"`
	Height           uint32    `json:"height,omitempty"`
	FPSNum           uint32    `json:"fps_num,omitempty"`
	FPSDen           uint32    `json:"fps_den,omitempty"`
	KeyframeInterval uint32    `json:"keyframe_interval,omitempty"`
	AudioCodec       string    `json:"audio_codec,omitempty"`
	AudioBitrate     string    `json:"audio_bitrate,omitempty"`
	SampleRate       uint32    `json:"sample_rate,omitempty"`
	Channels         uint32    `json:"channels,omitempty"`
	DurationSec      float64   `json:"duration_sec,omitempty"`
	KeepAudio        bool      `json:"keep_audio,omitempty"`
	NoAudio          bool      `json:"no_audio,omitempty"`
	OverlayPath      string    `json:"overlay_path,omitempty"`
	Opacity          float64   `json:"opacity,omitempty"`
	// Watermark scaling + green-screen chroma key (YouTube watermark flow).
	// ScalePercent is the overlay size as a percentage of the MAIN frame
	// width (0 = leave the overlay at its native size). GreenScreen* drive
	// the ffmpeg chromakey filter that removes the backdrop before the
	// alpha/opacity pass.
	ScalePercent          uint32             `json:"scale_percent,omitempty"`
	GreenScreenColor      string             `json:"green_screen_color,omitempty"`
	GreenScreenSimilarity float64            `json:"green_screen_similarity,omitempty"`
	GreenScreenBlend      float64            `json:"green_screen_blend,omitempty"`
	InputPaths            []string           `json:"input_paths,omitempty"`
	Jobs                  []cutRequestJob    `json:"jobs,omitempty"`
	NoTransitions         bool               `json:"no_transitions,omitempty"`
	ClipDurationSec       int                `json:"clip_duration_sec,omitempty"`
	NoEffects             bool               `json:"no_effects,omitempty"`
	Transitions           []renderTransition `json:"transitions,omitempty"`
	EffectPaths           []renderEffectPath `json:"effect_paths,omitempty"`
	OverlayOpacity        float64            `json:"overlay_opacity,omitempty"`
	Font                  string             `json:"font,omitempty"`
	Effects               []renderEffect     `json:"effects,omitempty"`
	Overlays              []renderOverlay    `json:"overlays,omitempty"`
	MaxDurationSec        float64            `json:"max_duration_sec,omitempty"`
	AudioPlan             json.RawMessage    `json:"audio_plan,omitempty"`
	AudioAssets           []audioAsset       `json:"audio_assets,omitempty"`
	// RenderPlan is the sealed generation-time contract. The Go adapter
	// validates its hashes and manifest files before this envelope is sent;
	// keeping the exact JSON here lets the executor audit the same plan.
	RenderPlan json.RawMessage `json:"render_plan,omitempty"`
	// ClipPlan is the sealed ClipRenderPlanV1 for render_clip. Validated in
	// this transport (decode + drift) before Rust is invoked; Rust re-audits
	// the same plan and verifies every referenced artifact fail-closed.
	ClipPlan json.RawMessage `json:"clip_plan,omitempty"`
	// RenderBackend is the backend resolved by the cliprender capability's
	// RenderBackendResolver (chronon_vulkan | cuda_native | ffmpeg_fallback).
	// Rust executes the selected backend verbatim; it never derives the
	// backend from the codec.
	RenderBackend string `json:"render_backend,omitempty"`
}

// Validate checks the transport envelope and the operation-specific required
// paths before the request reaches the Rust process. Capability-specific
// profile validation remains owned by the Rust execution contract.
func (r request) Validate() error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version: %q", r.Version)
	}
	if !r.Operation.valid() {
		return fmt.Errorf("unsupported media operation: %q", r.Operation)
	}
	requireSource := func() error {
		if strings.TrimSpace(r.SourcePath) == "" {
			return fmt.Errorf("%s: source_path is required", r.Operation)
		}
		return nil
	}
	requireOutput := func() error {
		if strings.TrimSpace(r.OutputPath) == "" {
			return fmt.Errorf("%s: output_path is required", r.Operation)
		}
		return nil
	}
	switch r.Operation {
	case OperationHealth:
		return nil
	case OperationMergeInputs:
		if strings.TrimSpace(r.OutputPath) == "" {
			return fmt.Errorf("%s: output_path is required", r.Operation)
		}
		if len(r.InputPaths) == 0 {
			return fmt.Errorf("%s: input_paths are required", r.Operation)
		}
		return nil
	case OperationRemoveSilence:
		if err := requireSource(); err != nil {
			return err
		}
		return requireOutput()
	case OperationRenderAudioPlan:
		if err := requireOutput(); err != nil {
			return err
		}
		if len(r.AudioPlan) == 0 || string(r.AudioPlan) == "null" {
			return fmt.Errorf("%s: audio_plan is required", r.Operation)
		}
		return nil
	case OperationMuxAudioCopy:
		if err := requireOutput(); err != nil {
			return err
		}
		if len(r.InputPaths) != 2 {
			return fmt.Errorf("%s: exactly video and final audio inputs are required", r.Operation)
		}
		return nil
	case OperationProbe:
		return requireSource()
	case OperationCutBatch:
		if err := requireSource(); err != nil {
			return err
		}
		if len(r.Jobs) == 0 {
			return fmt.Errorf("%s: jobs are required", r.Operation)
		}
		return nil
	case OperationRenderStock:
		if len(r.InputPaths) == 0 {
			return fmt.Errorf("%s: input_paths are required", r.Operation)
		}
		if len(r.RenderPlan) > 0 && string(r.RenderPlan) != "null" {
			var plan capabilityrender.RenderPlan
			if err := json.Unmarshal(r.RenderPlan, &plan); err != nil {
				return fmt.Errorf("%s: decode sealed render_plan: %w", r.Operation, err)
			}
			// This is the last Go transport boundary. Validate the complete
			// sealed contract here as well as in StockRenderer so every
			// caller of Client.call is fail-closed before Rust is invoked.
			if _, err := capabilityrender.ValidateRenderPlan(plan, filesystem.NewOS()); err != nil {
				return fmt.Errorf("%s: sealed render_plan validation failed: %w", r.Operation, err)
			}
		}
		return requireOutput()
	case OperationRenderClip:
		if err := requireSource(); err != nil {
			return err
		}
		if err := requireOutput(); err != nil {
			return err
		}
		if len(r.ClipPlan) == 0 || string(r.ClipPlan) == "null" {
			return fmt.Errorf("%s: clip_plan is required", r.Operation)
		}
		// Last Go transport boundary: decode the sealed ClipRenderPlanV1 and
		// re-validate the complete contract (identity + hashes + enum gates),
		// so a drifted/tampered plan is rejected before any Rust process
		// starts — the Rust side then re-audits the same plan fail-closed.
		var plan cliprender.ClipRenderPlanV1
		if err := json.Unmarshal(r.ClipPlan, &plan); err != nil {
			return fmt.Errorf("%s: decode sealed clip_plan: %w", r.Operation, err)
		}
		if err := plan.Validate(); err != nil {
			return fmt.Errorf("%s: sealed clip_plan validation failed: %w", r.Operation, err)
		}
		return nil
	case OperationAdminRender:
		if err := requireSource(); err != nil {
			return err
		}
		return requireOutput()
	case OperationCutCopy, OperationNormalize, OperationCutAndNormalize,
		OperationWatermark, OperationExtractFrame, OperationGenerateProxy,
		OperationGenerateStoryboard, OperationRemuxHLS, OperationTrim:
		if err := requireSource(); err != nil {
			return err
		}
		return requireOutput()
	default:
		return fmt.Errorf("unsupported media operation: %q", r.Operation)
	}
}

type audioAsset struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
}

type renderTransition struct {
	ClipIndex int    `json:"clip_index"`
	Segment   string `json:"segment"`
	ID        string `json:"id"`
}

type renderEffectPath struct {
	ClipIndex int    `json:"clip_index"`
	Path      string `json:"path"`
}

type renderEffect struct {
	Path     string  `json:"path"`
	DelayMS  int     `json:"delay_ms"`
	Duration float64 `json:"duration"`
	Volume   string  `json:"volume"`
}

type renderOverlay struct {
	Text  string `json:"text"`
	Start string `json:"start"`
	End   string `json:"end"`
	Size  string `json:"size"`
	Y     string `json:"y"`
	Color string `json:"color"`
}

type response struct {
	OK         bool           `json:"ok"`
	Operation  string         `json:"operation"`
	SourcePath string         `json:"source_path"`
	Items      []cutItem      `json:"items"`
	Metadata   *mediaMetadata `json:"metadata"`
	// Metrics carries the operation's real consumption, measured in the
	// process that owns the work: the Rust media executor reports its child
	// FFmpeg wall/CPU time, input/output bytes, cache outcome and frame
	// counts. Nil (absent) means the executor did not report metrics; the
	// Go boundary then falls back to its own measurements.
	Metrics *OperationMetrics `json:"metrics,omitempty"`
	Error   string            `json:"error"`
}

// OperationMetrics is the standard per-operation metrics block returned by
// the Rust media executor. CPU time is the child FFmpeg consumption (not an
// estimate from wall time × CPU %), so concurrent renders attribute cost to
// the right operation. Fields are zero when not applicable (e.g. frames stay
// 0 on copy-only mux paths).
type OperationMetrics struct {
	WallMS         int64 `json:"wall_ms"`
	CPUUserMS      int64 `json:"cpu_user_ms"`
	CPUSystemMS    int64 `json:"cpu_system_ms"`
	InputBytes     int64 `json:"input_bytes"`
	OutputBytes    int64 `json:"output_bytes"`
	CacheHit       bool  `json:"cache_hit"`
	FramesDecoded  int64 `json:"frames_decoded"`
	FramesEncoded  int64 `json:"frames_encoded"`
	PeakRSSBytes   int64 `json:"peak_rss_bytes"`
	DiskReadBytes  int64 `json:"disk_read_bytes"`
	DiskWriteBytes int64 `json:"disk_write_bytes"`
	NetworkRXBytes int64 `json:"network_rx_bytes"`
	NetworkTXBytes int64 `json:"network_tx_bytes"`
}

type cutRequestJob struct {
	JobID      string  `json:"job_id"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
	OutputPath string  `json:"output_path"`
}

type cutItem struct {
	JobID       string  `json:"job_id"`
	OutputPath  string  `json:"output_path"`
	Status      string  `json:"status"`
	SizeBytes   int64   `json:"size_bytes"`
	DurationSec float64 `json:"duration_sec"`
	Error       string  `json:"error"`
}

type mediaMetadata struct {
	DurationSec      float64 `json:"duration_sec"`
	Bitrate          int64   `json:"bitrate"`
	Width            uint32  `json:"width"`
	Height           uint32  `json:"height"`
	FPS              float64 `json:"fps"`
	VideoCodec       string  `json:"video_codec"`
	PixelFormat      string  `json:"pixel_format"`
	FormatName       string  `json:"format_name"`
	StreamCount      uint32  `json:"stream_count"`
	VideoStreamCount uint32  `json:"video_stream_count"`
	AudioStreamCount uint32  `json:"audio_stream_count"`
	FPSNum           uint32  `json:"fps_num"`
	FPSDen           uint32  `json:"fps_den"`
	AudioCodec       string  `json:"audio_codec"`
	AudioProfile     string  `json:"audio_profile"`
	SampleRate       uint32  `json:"sample_rate"`
	Channels         uint32  `json:"channels"`
	StartPTS         int64   `json:"start_pts"`
	HasVideo         bool    `json:"has_video"`
	HasAudio         bool    `json:"has_audio"`
	// Stage timings populated only by render_audio_plan (mix → AAC encode →
	// probe → hash). Zero everywhere else; final_audio_sha256 is the digest
	// Rust computed over the published output.
	MixMS       int64 `json:"mix_ms"`
	AACEncodeMS int64 `json:"aac_encode_ms"`
	ProbeMS     int64 `json:"probe_ms"`
	HashMS      int64 `json:"hash_ms"`
	FFmpegMS    int64 `json:"ffmpeg_ms"`
	// render_clip phase timings (the benchmark decomposition): StartupMS is
	// the pre-ffmpeg wall inside Rust (plan decode + source probe + graph
	// build + process spawn), PublishMS the output publish, OpMS the whole
	// render_clip wall. Pointers: nil when the operation did not report the
	// phase (NOT_INSTRUMENTED semantics — never a fake zero).
	StartupMS        *int64 `json:"startup_ms,omitempty"`
	PublishMS        *int64 `json:"publish_ms,omitempty"`
	OpMS             *int64 `json:"op_ms,omitempty"`
	FinalAudioSHA256 string `json:"final_audio_sha256"`
	// render_clip audio copy policy outcome (copy verbatim vs one certified
	// conversion) and whether the burn stage rasterized libass (CPU).
<<<<<<< Updated upstream
	AudioCopyEligible *bool   `json:"audio_copy_eligible,omitempty"`
	AudioEncodePasses *int    `json:"audio_encode_passes,omitempty"`
	SubtitleRasterCPU *bool   `json:"subtitle_raster_cpu,omitempty"`
	GPUCopyBytes      *uint64 `json:"gpu_copy_bytes,omitempty"`
	VideoZeroCopy     *bool   `json:"video_zero_copy,omitempty"`
||||||| constructed merge base
	AudioCopyEligible *bool   `json:"audio_copy_eligible,omitempty"`
	AudioEncodePasses *int    `json:"audio_encode_passes,omitempty"`
	SubtitleRasterCPU *bool   `json:"subtitle_raster_cpu,omitempty"`
	GPUCopyBytes      *uint64 `json:"gpu_copy_bytes,omitempty"`
=======
	AudioCopyEligible       *bool    `json:"audio_copy_eligible,omitempty"`
	AudioEncodePasses       *int     `json:"audio_encode_passes,omitempty"`
	SubtitleRasterCPU       *bool    `json:"subtitle_raster_cpu,omitempty"`
	GPUCopyBytes            *uint64  `json:"gpu_copy_bytes,omitempty"`
	GPUUploadBytes          *uint64  `json:"gpu_upload_bytes,omitempty"`
	GPUReadbackBytes        *uint64  `json:"gpu_readback_bytes,omitempty"`
	EncoderStagingCopyBytes *uint64  `json:"encoder_staging_copy_bytes,omitempty"`
	NV12ToRGBAFrames        *uint64  `json:"nv12_to_rgba_frames,omitempty"`
	RGBAToNV12Frames        *uint64  `json:"rgba_to_nv12_frames,omitempty"`
	CUDACompositeFrames     *uint64  `json:"cuda_composite_frames,omitempty"`
	GPUUtilizationAvg       *float64 `json:"gpu_utilization_avg,omitempty"`
	GPUUtilizationPeak      *float64 `json:"gpu_utilization_peak,omitempty"`
	NVENCUtilizationAvg     *float64 `json:"nvenc_utilization_avg,omitempty"`
	NVDECUtilizationAvg     *float64 `json:"nvdec_utilization_avg,omitempty"`
	VRAMUsedPeakMB          *uint64  `json:"vram_used_peak_mb,omitempty"`
>>>>>>> Stashed changes
	// Fine-grained render phases are optional until the owning executor
	// measures them; nil preserves NOT_INSTRUMENTED semantics.
	DecodeMS          *int64 `json:"decode_ms,omitempty"`
	FilterGraphMS     *int64 `json:"filter_graph_ms,omitempty"`
	SubtitleRasterMS  *int64 `json:"subtitle_raster_ms,omitempty"`
	WatermarkRasterMS *int64 `json:"watermark_raster_ms,omitempty"`
	FrameConversionMS *int64 `json:"frame_conversion_ms,omitempty"`
	EncodeMS          *int64 `json:"encode_ms,omitempty"`
	AudioMuxMS        *int64 `json:"audio_mux_ms,omitempty"`
}

// Wire DTOs for mediaexec.v1. These types intentionally contain only the
// transport contract; capability adapters live in the neighboring files.

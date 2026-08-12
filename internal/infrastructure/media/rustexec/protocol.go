package rustexec

import (
	"encoding/json"
	"fmt"
	"strings"

	capabilityrender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
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
		OperationRemuxHLS, OperationTrim, OperationRenderStock, OperationAdminRender,
		OperationMergeInputs, OperationRemoveSilence, OperationRenderAudioPlan, OperationMuxAudioCopy:
		return true
	default:
		return false
	}
}

type request struct {
	Version          string             `json:"version"`
	Operation        Operation          `json:"operation"`
	FFmpegPath       string             `json:"ffmpeg_path,omitempty"`
	SourcePath       string             `json:"source_path,omitempty"`
	OutputPath       string             `json:"output_path,omitempty"`
	TimestampSec     float64            `json:"timestamp_sec,omitempty"`
	StartSec         float64            `json:"start_sec,omitempty"`
	EndSec           float64            `json:"end_sec,omitempty"`
	IntervalFrames   uint32             `json:"interval_frames,omitempty"`
	Columns          uint32             `json:"columns,omitempty"`
	Rows             uint32             `json:"rows,omitempty"`
	Codec            string             `json:"codec,omitempty"`
	Preset           string             `json:"preset,omitempty"`
	CRF              int                `json:"crf,omitempty"`
	Width            uint32             `json:"width,omitempty"`
	Height           uint32             `json:"height,omitempty"`
	FPS              uint32             `json:"fps,omitempty"`
	KeyframeInterval uint32             `json:"keyframe_interval,omitempty"`
	AudioCodec       string             `json:"audio_codec,omitempty"`
	AudioBitrate     string             `json:"audio_bitrate,omitempty"`
	SampleRate       uint32             `json:"sample_rate,omitempty"`
	Channels         uint32             `json:"channels,omitempty"`
	DurationSec      float64            `json:"duration_sec,omitempty"`
	KeepAudio        bool               `json:"keep_audio,omitempty"`
	NoAudio          bool               `json:"no_audio,omitempty"`
	OverlayPath      string             `json:"overlay_path,omitempty"`
	Opacity          float64            `json:"opacity,omitempty"`
	InputPaths       []string           `json:"input_paths,omitempty"`
	Jobs             []cutRequestJob    `json:"jobs,omitempty"`
	NoTransitions    bool               `json:"no_transitions,omitempty"`
	ClipDurationSec  int                `json:"clip_duration_sec,omitempty"`
	NoEffects        bool               `json:"no_effects,omitempty"`
	Transitions      []renderTransition `json:"transitions,omitempty"`
	EffectPaths      []renderEffectPath `json:"effect_paths,omitempty"`
	OverlayOpacity   float64            `json:"overlay_opacity,omitempty"`
	Font             string             `json:"font,omitempty"`
	Effects          []renderEffect     `json:"effects,omitempty"`
	Overlays         []renderOverlay    `json:"overlays,omitempty"`
	MaxDurationSec   float64            `json:"max_duration_sec,omitempty"`
	AudioPlan        json.RawMessage    `json:"audio_plan,omitempty"`
	AudioAssets      []audioAsset       `json:"audio_assets,omitempty"`
	// RenderPlan is the sealed generation-time contract. The Go adapter
	// validates its hashes and manifest files before this envelope is sent;
	// keeping the exact JSON here lets the executor audit the same plan.
	RenderPlan json.RawMessage `json:"render_plan,omitempty"`
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
			if _, err := capabilityrender.ValidateRenderPlan(plan); err != nil {
				return fmt.Errorf("%s: sealed render_plan validation failed: %w", r.Operation, err)
			}
		}
		return requireOutput()
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
	Error      string         `json:"error"`
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
	DurationSec  float64 `json:"duration_sec"`
	Bitrate      int64   `json:"bitrate"`
	Width        uint32  `json:"width"`
	Height       uint32  `json:"height"`
	FPS          float64 `json:"fps"`
	VideoCodec   string  `json:"video_codec"`
	PixelFormat  string  `json:"pixel_format"`
	AudioCodec   string  `json:"audio_codec"`
	AudioProfile string  `json:"audio_profile"`
	SampleRate   uint32  `json:"sample_rate"`
	Channels     uint32  `json:"channels"`
	StartPTS     int64   `json:"start_pts"`
	HasVideo     bool    `json:"has_video"`
	HasAudio     bool    `json:"has_audio"`
}

// Wire DTOs for mediaexec.v1. These types intentionally contain only the
// transport contract; capability adapters live in the neighboring files.

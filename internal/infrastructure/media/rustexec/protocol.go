package rustexec

type request struct {
	Version          string             `json:"version"`
	Operation        string             `json:"operation"`
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
	KeyframeInterval uint32             `json:"keyframe_interval,omitempty"`
	Font             string             `json:"font,omitempty"`
	Effects          []renderEffect     `json:"effects,omitempty"`
	Overlays         []renderOverlay    `json:"overlays,omitempty"`
	MaxDurationSec   float64            `json:"max_duration_sec,omitempty"`
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
	DurationSec float64 `json:"duration_sec"`
	Width       uint32  `json:"width"`
	Height      uint32  `json:"height"`
	FPS         float64 `json:"fps"`
	VideoCodec  string  `json:"video_codec"`
	AudioCodec  string  `json:"audio_codec"`
	SampleRate  uint32  `json:"sample_rate"`
	Channels    uint32  `json:"channels"`
	HasVideo    bool    `json:"has_video"`
	HasAudio    bool    `json:"has_audio"`
}

// Wire DTOs for mediaexec.v1. These types intentionally contain only the
// transport contract; capability adapters live in the neighboring files.

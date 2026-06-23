package config

// VideoConfig holds all video processing parameters shared across the clip, stock,
// and video rendering pipelines. Centralizing these values ensures that every
// stage uses the same codec, resolution, and preset so that ffmpeg can perform
// fast stream-copy concatenation without re-encoding.
type VideoConfig struct {
	Width             int      `yaml:"width" default:"1920"`
	Height            int      `yaml:"height" default:"1080"`
	FPS               int      `yaml:"fps" default:"30"`
	Codec             string   `yaml:"codec" default:"h264_nvenc"`
	Preset            string   `yaml:"preset" default:"p1"`
	CRF               int      `yaml:"crf" default:"23"`
	Duration          int      `yaml:"duration" default:"7"`
	KeyframeInterval  int      `yaml:"keyframe_interval" default:"60"`
	AudioCodec        string   `yaml:"audio_codec" default:"aac"`
	AudioBitrate      string   `yaml:"audio_bitrate" default:"128k"`
	ClipDuration      int      `yaml:"clip_duration" default:"5"`
	ChunkDuration     int      `yaml:"chunk_duration" default:"25"`
	MaxClipsPerSource int      `yaml:"max_clips_per_source" default:"30"`
	SearchCount       int      `yaml:"search_count" default:"25"`
	OverlayOpacity    float64  `yaml:"overlay_opacity" default:"0.25"`
	EffectInterval     int      `yaml:"effect_interval" default:"4"`
	TransitionInterval int      `yaml:"transition_interval" default:"4"`
	TransitionPresets  []string `yaml:"transition_presets"`
}

// WithDefaults returns a copy of VideoConfig with zero-values replaced by defaults.
func (v VideoConfig) WithDefaults() VideoConfig {
	if v.Width <= 0 {
		v.Width = 1920
	}
	if v.Height <= 0 {
		v.Height = 1080
	}
	if v.FPS <= 0 {
		v.FPS = 30
	}
	if v.Duration <= 0 {
		v.Duration = 7
	}
	if v.Codec == "" {
		v.Codec = "h264_nvenc"
	}
	if v.Preset == "" {
		v.Preset = "p1"
	}
	if v.CRF <= 0 {
		v.CRF = 23
	}
	if v.KeyframeInterval <= 0 {
		v.KeyframeInterval = 60
	}
	if v.AudioCodec == "" {
		v.AudioCodec = "aac"
	}
	if v.AudioBitrate == "" {
		v.AudioBitrate = "128k"
	}
	if v.ClipDuration <= 0 {
		v.ClipDuration = 5
	}
	if v.ChunkDuration <= 0 {
		v.ChunkDuration = 25
	}
	if v.MaxClipsPerSource <= 0 {
		v.MaxClipsPerSource = 30
	}
	if v.SearchCount <= 0 {
		v.SearchCount = 25
	}
	// Note: OverlayOpacity == 0 means fully transparent (no overlay); < 0 means use default.
	if v.OverlayOpacity < 0 {
		v.OverlayOpacity = 0.25
	}
	// Note: EffectInterval == 0 means no effects; < 0 means use default.
	if v.EffectInterval < 0 {
		v.EffectInterval = 4
	}
	// Note: TransitionInterval == 0 means no transitions; < 0 means use default.
	if v.TransitionInterval < 0 {
		v.TransitionInterval = 4
	}
	if len(v.TransitionPresets) == 0 {
		v.TransitionPresets = []string{
			"fade", "fadeblack", "fadewhite",
			"slideleft", "slideright", "slideup", "slidedown",
			"circleclose", "circleopen",
			"horzclose", "horzopen", "vertclose", "vertopen",
			"dissolve", "pixelize",
			"wipeleft", "wiperight", "wipeup", "wipedown",
			"smoothleft", "smoothright", "smoothup", "smoothdown",
			"radial", "hblur", "fadegrays",
			"squeezeh", "squeezev",
		}
	}
	return v
}

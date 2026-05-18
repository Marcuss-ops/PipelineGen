package tachyon_spec

// MediaTimelinePlan represents the main scene plan for TACHYON
type MediaTimelinePlan struct {
	Tracks         []VideoTrack   `json:"tracks"`
	Effects        []EffectNode   `json:"effects,omitempty"`
	Overlays       []OverlayTrack `json:"overlays,omitempty"`
	Output         OutputConfig   `json:"output"`
	Threads        int            `json:"threads,omitempty"`
	Timebase       Timebase       `json:"timebase,omitempty"`
	EmergencyMode  bool           `json:"emergency_fallback_enabled,omitempty"`
}

type Timebase struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

type OutputConfig struct {
	Path         string `json:"path"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPS          int    `json:"fps"`
	CRF          int    `json:"crf"`
	VideoCodec   string `json:"video_codec,omitempty"`
	AudioCodec   string `json:"audio_codec,omitempty"`
	AudioBitrate string `json:"audio_bitrate,omitempty"`
	SampleRate   int    `json:"sample_rate,omitempty"`
}

type VideoTrack struct {
	IsPrimary bool           `json:"is_primary"`
	Segments  []VideoSegment `json:"segments"`
}

type VideoSegment struct {
	Path          string      `json:"path"`
	Name          string      `json:"name,omitempty"`
	Start         float64     `json:"start"`          // start time in the source file
	Duration      float64     `json:"duration"`       // duration to cut
	TimelineStart float64     `json:"timeline_start"` // where it starts on the timeline
	Transition    *Transition `json:"transition,omitempty"`
}

type Transition struct {
	Type     string            `json:"type"` // e.g., "circle_iris", "glitch_slice"
	Duration float64           `json:"duration"`
	Params   map[string]string `json:"params,omitempty"`
}

type EffectNode struct {
	Type       string            `json:"type"` // e.g., "wipe", "fade"
	Start      float64           `json:"start"`
	Duration   float64           `json:"duration"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type OverlayTrack struct {
	Path      string  `json:"path"`
	StartTime float64 `json:"start_time"`
	Duration  float64 `json:"duration"`
	Opacity   float64 `json:"opacity"`
	X         int     `json:"x,omitempty"`
	Y         int     `json:"y,omitempty"`
}

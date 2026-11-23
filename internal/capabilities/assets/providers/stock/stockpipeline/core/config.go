package core

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration      int
	MaxResults         int
	EffectInterval     int
	EffectsDir         string
	Width              int
	Height             int
	FPSNum             int
	FPSDen             int
	Codec              string
	Preset             string
	CRF                int
	KeyframeInterval   int
	TransitionInterval int
	OverlayOpacity     float64
	ClipDuration       int
}

// DefaultPipelineConfig returns the CPU-first stock rendering profile.
//
// libx264 is the canonical default because stock runs are headless and must
// not require NVIDIA hardware or drivers. The infrastructure renderer still
// accepts explicit hardware codec overrides (for example h264_nvenc), but
// callers must opt into those profiles deliberately.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
		Width:          1920, Height: 1080, FPSNum: 24, FPSDen: 1, Codec: "libx264", Preset: "veryfast", CRF: 23,
		KeyframeInterval: 48, TransitionInterval: 4, OverlayOpacity: 0.25, ClipDuration: 5,
	}
}

package stockpipeline

type PipelineConfig struct {
	ChunkDuration  int    // max 25 seconds per output chunk
	MaxResults     int    // max videos to fetch per search query
	EffectInterval int    // apply overlay effect every N clips (0 = disabled)
	EffectsDir     string // directory containing effect overlay .mp4 files
}

func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
	}
}

type ChunkResult struct {
	Index         int
	TimelineStart float64
	TimelineEnd   float64
	LocalPath     string
	DriveLink     string
	DownloadLink  string
	DriveFileID   string
	Title         string
}

type PipelineResult struct {
	Chunks      []ChunkResult
	TotalClips  int
	TotalChunks int
	SearchTerms []string
}

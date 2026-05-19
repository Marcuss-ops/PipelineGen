package stockpipeline

type PipelineConfig struct {
	SegmentDuration int     // default 5 seconds per clip segment
	ChunkDuration   int     // default 30 seconds per output chunk
	TransitionEvery int     // add random transition every N clips
	BrightnessEvery int     // add brightness overlay every N clips
	BrightnessLevel float64 // 0.0-1.0, default 0.25
	MaxResults      int     // max videos to fetch per search query
}

func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		SegmentDuration: 5,
		ChunkDuration:   30,
		TransitionEvery: 5,
		BrightnessEvery: 3,
		BrightnessLevel: 0.25,
		MaxResults:      20,
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

package stockpipeline

// StockRunPayload is the job payload for media.stock jobs.
// It was previously in internal/core/jobs/payloads.go (deleted in PR4).
type StockRunPayload struct {
	SearchQueries []string                  `json:"search_queries"`
	DirectURLs    []string                  `json:"direct_urls,omitempty"`
	TotalMinutes  int                       `json:"total_minutes"`
	ChunkDuration int                       `json:"chunk_duration,omitempty"`
	ClipDuration  int                       `json:"clip_duration,omitempty"`
	NoAudio       bool                      `json:"no_audio,omitempty"`
	NoEffects     bool                      `json:"no_effects,omitempty"`
	NoTransitions bool                      `json:"no_transitions,omitempty"`
	MaxVideos     int                       `json:"max_videos,omitempty"`
	Subfolder     string                    `json:"subfolder"`
	FolderName    string                    `json:"folder_name"`
	FolderID      string                    `json:"folder_id,omitempty"`
	Metadata      *StockRunPayloadMetadata  `json:"metadata,omitempty"`
}

// StockRunPayloadMetadata mirrors ChunkMetadataInput for JSON compatibility.
type StockRunPayloadMetadata struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

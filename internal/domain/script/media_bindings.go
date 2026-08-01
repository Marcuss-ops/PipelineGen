package script

// ImageBinding holds the metadata for an AI-generated scene image.
// The LLM produces the prompt; the application layer fills in the
// generated asset URL and local path.
type ImageBinding struct {
	// ImageID is the canonical asset ID of the generated image.
	ImageID string `json:"image_id,omitempty"`

	// Prompt is the image generation prompt produced by the LLM.
	Prompt string `json:"prompt,omitempty"`

	// URL is the publicly-accessible URL of the generated image,
	// set by the image postprocessor.
	URL string `json:"url,omitempty"`

	// LocalPath is the local filesystem path to the generated image,
	// set by the image postprocessor after download.
	LocalPath string `json:"local_path,omitempty"`

	// Status tracks the generation outcome: "pending", "generated",
	// "failed".
	Status string `json:"status,omitempty"`
}

// VoiceoverBinding holds the metadata for a generated voiceover
// audio track. The LLM does not produce this; it is created
// exclusively by the voiceover postprocessor.
type VoiceoverBinding struct {
	// Status tracks the generation outcome: "pending", "completed",
	// "failed".
	Status string `json:"status"`

	// Link is the publicly-accessible URL of the generated audio.
	Link string `json:"link,omitempty"`

	// LocalPath is the local filesystem path to the generated audio.
	LocalPath string `json:"local_path,omitempty"`

	// DurationMs is the audio duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// StockBinding binds a scene to a semantically associated stock
// footage asset. Populated by the legacy stock binding surface
// which searches Qdrant per-scene and falls back to the clip
// drive link when no stock match is found.
type StockBinding struct {
	// AssetID is the canonical media_assets.id of the matched stock.
	AssetID string `json:"asset_id,omitempty"`

	// Name is the human-readable name of the stock asset.
	Name string `json:"name,omitempty"`

	// Source identifies the provider (artlist|youtube|stock).
	Source string `json:"source,omitempty"`

	// DriveLink is the Google Drive URL of the stock asset.
	DriveLink string `json:"drive_link,omitempty"`
	FolderID  string `json:"folder_id,omitempty"`

	// Score is the cosine-similarity from the vector search.
	Score float64 `json:"score,omitempty"`

	// Fallback is true when the drive_link comes from the scene's
	// ClipBinding.DriveLink because no stock match was found.
	Fallback   bool  `json:"fallback"`
	StartMs    int64 `json:"start_ms,omitempty"`
	EndMs      int64 `json:"end_ms,omitempty"`
	DurationMs int64 `json:"duration_ms,omitempty"`
}

package script

import ()

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

	// Links contains the published Drive link for each generated language.
	// Link remains the compatibility/default-language surface.
	Links map[string]string `json:"links,omitempty"`

	// LocalPath is the local filesystem path to the generated audio.
	LocalPath string `json:"local_path,omitempty"`

	// DurationMs is the audio duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// Timing maps the published timing bundle (timing.json SSOT + optional
	// SRT/VTT projections + hashes) per generated language. Populated by
	// the voiceover postprocessor from the canonical per-item timing
	// result; preserved by clone / merge / translation write-back /
	// persistence so previously produced timing links are never erased.
	Timing map[string]VoiceoverTimingBinding `json:"timing,omitempty"`
}

// VoiceoverTimingBinding carries the published timing bundle references
// for one voiceover language. It mirrors the per-item timing result: the
// JSON artifact is the SSOT and SRT/VTT are display projections; the
// SHA-256 hashes bind the artifact to exactly one synthesized text and
// one final audio file. Word-level timing is intentionally NOT inlined
// here — the canonical word array lives in the published timing.json.
type VoiceoverTimingBinding struct {
	// Status is "completed" | "unavailable" | "failed" (godlike/07
	// no-fake-availability: an absent timing is explicit, never silent).
	Status string `json:"status,omitempty"`

	// JSONLink / SRTLink / VTTLink are the verified Drive links of the
	// published timing projections (only the formats actually requested).
	JSONLink string `json:"json_link,omitempty"`
	SRTLink  string `json:"srt_link,omitempty"`
	VTTLink  string `json:"vtt_link,omitempty"`

	// BoundaryMode is the captured boundary granularity ("word").
	BoundaryMode string `json:"boundary_mode,omitempty"`

	// WordCount is the number of word boundaries in the artifact.
	WordCount int `json:"word_count,omitempty"`
	// DurationUS is the final audio duration in microseconds.
	DurationUS int64 `json:"duration_us,omitempty"`

	// TextSHA256 binds the artifact to the exact synthesized text.
	TextSHA256 string `json:"text_sha256,omitempty"`
	// AudioSHA256 binds the artifact to the exact final audio bytes.
	AudioSHA256 string `json:"audio_sha256,omitempty"`
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
	DriveLink  string `json:"drive_link,omitempty"`
	FolderID   string `json:"folder_id,omitempty"`
	FolderLink string `json:"folder_link,omitempty"`

	// Score is the cosine-similarity from the vector search.
	Score float64 `json:"score,omitempty"`

	// Fallback is true when the drive_link comes from the scene's
	// ClipBinding.DriveLink because no stock match was found.
	Fallback   bool  `json:"fallback"`
	StartMs    int64 `json:"start_ms,omitempty"`
	EndMs      int64 `json:"end_ms,omitempty"`
	DurationMs int64 `json:"duration_ms,omitempty"`
}

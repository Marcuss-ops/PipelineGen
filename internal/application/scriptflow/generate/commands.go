// Package generate implements script generation orchestration (use case layer).
//
// This package owns the validation, defaults, payload construction, and job
// enqueue logic for script generation. It does NOT import HTTP types from
// internal/api/ — the thin transport layer in internal/api/script/ maps
// HTTP request/response types to/from these application commands/results.
//
// See docs/api-package-boundaries.md for the full architecture.
package generate

// FromClipsCommand is the application-layer input for generating a script
// from clips or from a text topic.
type FromClipsCommand struct {
	Topic      string
	SourceText string
	Guidelines string

	ClipIDs  []string
	NumClips int

	Title         string
	OutputName    string
	Language      string
	Tone          string
	Style         string
	Model         string
	DriveFolderID string

	TargetWords       int
	Duration          int
	MinWords          int
	SentencesPerImage int
	ImagesPerScene    int

	ExtractEntities     bool
	ArtlistSearch       bool
	StockSearch         bool
	GenerateMetadata    bool
	GenerateVoiceover   bool
	VoiceoverGroup      string
	VoiceoverFolderID   string
	GenerateSceneImages bool

	Languages []string

	TranscriptPolicy string
	OrderingStrategy string

	SaveToDB         bool
	GenerateTimeline bool
	ForceRefresh     bool

	MinQualityScore    *float64
	MinTranscriptWords *int

	PromptVersion       string
	EditorPromptVersion string
	QAPromptVersion     string
}

// FromClipsResult is the application-layer result for FromClips generation.
type FromClipsResult struct {
	OK        bool
	JobID     string
	Status    string
	ClipCount int
}

// WithImagesCommand is the application-layer input for generating a script
// with scene-by-scene AI images.
type WithImagesCommand struct {
	Topic      string
	SourceText string
	Guidelines string

	ClipIDs  []string
	NumClips int

	Title         string
	OutputName    string
	Language      string
	Tone          string
	Style         string
	Model         string
	DriveFolderID string

	TargetWords       int
	Duration          int
	MinWords          int
	SentencesPerImage int
	ImagesPerScene    int

	ArtlistSearch     bool
	StockSearch       bool
	GenerateVoiceover bool
	VoiceoverGroup    string
	VoiceoverFolderID string

	Languages []string

	TranscriptPolicy string
	OrderingStrategy string

	SaveToDB         bool
	GenerateTimeline bool
	ForceRefresh     bool

	MinQualityScore    *float64
	MinTranscriptWords *int

	PromptVersion       string
	EditorPromptVersion string
	QAPromptVersion     string
}

// BatchResult is the application-layer result for an async batch enqueue.
// TODO(PR-4): populated when batch async path is extracted from ScriptFlowHandler.
type BatchResult struct {
	OK        bool
	Async     bool
	JobID     string
	Status    string
	Message   string
	StatusURL string
}

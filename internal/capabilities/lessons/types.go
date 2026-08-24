// Package lessons provides structured web lesson generation from source text.
// It splits source text into chapters, generates each chapter in parallel
// using Ollama Chat API, and optionally generates AI images per chapter.
package lessons

// LessonRequest is the input for generating a web lesson.
type LessonRequest struct {
	SourceText     string `json:"source_text"`
	Title          string `json:"title"`
	Language       string `json:"language,omitempty"`
	Tone           string `json:"tone,omitempty"`
	MaxChapters    int    `json:"max_chapters,omitempty"`
	GenerateImages bool   `json:"generate_images,omitempty"`
	ImageStyle     string `json:"image_style,omitempty"`
	ImageWidth     int    `json:"image_width,omitempty"`
	ImageHeight    int    `json:"image_height,omitempty"`
	GeneratePDF    bool   `json:"generate_pdf,omitempty"`
	OllamaURL      string `json:"ollama_url,omitempty"`
	Async          bool   `json:"async,omitempty"`
}

// ChapterSplit represents a source text segment to be processed as a chapter.
type ChapterSplit struct {
	Index int
	Title string
	Text  string
}

// ChapterResult is the output of a single chapter generation.
type ChapterResult struct {
	Index     int       `json:"index"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	WordCount int       `json:"word_count"`
	Image     *ImageRef `json:"image,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// ImageRef holds a reference to a generated AI image.
type ImageRef struct {
	Hash        string `json:"hash"`
	PathRel     string `json:"path_rel"`
	URL         string `json:"url"`
	DriveLink   string `json:"drive_link,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Prompt      string `json:"prompt"`
}

// LessonResult is the complete result of lesson generation.
type LessonResult struct {
	Success        bool            `json:"success"`
	Title          string          `json:"title"`
	Language       string          `json:"language"`
	Chapters       []ChapterResult `json:"chapters"`
	TotalWords     int             `json:"total_words"`
	MarkdownPath   string          `json:"markdown_path,omitempty"`
	PDFPath        string          `json:"pdf_path,omitempty"`
	DriveDocURL    string          `json:"drive_doc_url,omitempty"`
	DriveFolderURL string          `json:"drive_folder_url,omitempty"`
	GeneratedAt    string          `json:"generated_at"`
	Error          string          `json:"error,omitempty"`
}

// LessonsConfig holds configuration for the lessons service.
type LessonsConfig struct {
	Enabled         bool   `yaml:"enabled"`
	DefaultModel    string `yaml:"default_model"`
	DefaultTone     string `yaml:"default_tone"`
	DefaultLanguage string `yaml:"default_language"`
	// DefaultImageModel is retained for config compatibility and is fixed to
	// the only supported Google Slides model.
	DefaultImageModel   string `yaml:"default_image_model"`
	MaxParallelChapters int    `yaml:"max_parallel_chapters"`
	OllamaURL           string `yaml:"ollama_url"`
	DriveFolderID       string `yaml:"drive_folder_id"`
}

// DefaultConfig returns sensible defaults for the lessons service.
func DefaultConfig() *LessonsConfig {
	return &LessonsConfig{
		Enabled:             true,
		DefaultModel:        "gemma4:e4b",
		DefaultTone:         "educational",
		DefaultLanguage:     "it",
		DefaultImageModel:   "nano-banana-pro",
		MaxParallelChapters: 5,
		OllamaURL:           "http://127.0.0.1:11434",
		DriveFolderID:       "",
	}
}

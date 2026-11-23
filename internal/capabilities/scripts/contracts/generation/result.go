package generation

import (
	scenecontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Result is the generation-phase projection of GenerateResult: the narration,
// its editorial takeaways and build provenance. Scenes stay in the scene
// contract; timing, audio, overlay and final-job receipts stay in their phase.
type Result struct {
	Output         Output                 `json:"output"`
	Title          string                 `json:"title,omitempty"`
	SourceLanguage scenecontract.Language `json:"source_language,omitempty"`
	OutputName     string                 `json:"output_name,omitempty"`
	WordCount      int                    `json:"word_count"`
	VoiceoverGroup string                 `json:"voiceover_group,omitempty"`

	Summary         string                        `json:"summary,omitempty"`
	BulletPoints    []string                      `json:"bullet_points,omitempty"`
	HeavySentences  []scriptpkg.ImportantSentence `json:"heavy_sentences,omitempty"`
	ChapterManifest *scriptpkg.ChapterManifest    `json:"chapter_manifest,omitempty"`
	SceneHighlights []scriptpkg.SceneHighlight    `json:"scene_highlights,omitempty"`
	Editorial       *scriptpkg.EditorialManifest  `json:"editorial,omitempty"`

	BinarySHA256     string `json:"binary_sha256,omitempty"`
	RustRelease      string `json:"rust_release,omitempty"`
	RustGitSHA       string `json:"rust_git_sha,omitempty"`
	RustBinarySHA256 string `json:"rust_binary_sha256,omitempty"`

	TranslationMetrics TranslationMetrics `json:"translation_metrics,omitempty"`
}

// TranslationMetrics is the read-only translation projection owned by the
// generation phase. The observability database remains the measurement
// authority.
type TranslationMetrics struct {
	Calls       int   `json:"calls"`
	Concurrency int   `json:"concurrency"`
	WallMS      int64 `json:"wall_ms"`
}

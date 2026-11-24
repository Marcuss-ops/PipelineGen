package contract

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text              string
	WordCount         int
	SpecScene         scriptpkg.SpecSceneOutput
	OriginalText      string
	OriginalSpecScene scriptpkg.SpecSceneOutput
	// TranslatedText and TranslatedSpecScene are the canonical translated
	// surfaces retained across postprocessor write-back.
	TranslatedText      string
	TranslatedSpecScene scriptpkg.SpecSceneOutput
	ModelUsed           string
	CacheStatus         string
	SourceTrace         *scriptpkg.ClipEvidence
	PriorArtifacts      map[string]PostProcessResult
	EffectiveLanguage   string
	StockEnabled        scriptpkg.Toggle
	StockBindings       []scriptpkg.StockBindingInput
	ResearchSources     []scriptpkg.SourceReference

	// Entities carries the entity-extraction result.
	Entities *scriptpkg.EntityResult

	// VidRushSegments carries the per-segment VidRush output.
	VidRushSegments []scriptpkg.VidRushSegmentResult

	// Metadata carries the video-metadata result.
	Metadata []scriptpkg.VideoMetadata

	// Provenance carries the generation provenance block.
	Provenance *scriptpkg.GenerationProvenance

	// SpecSceneChanged forces a document refresh after reconciliation.
	SpecSceneChanged bool `json:"-"`
}

// PostProcessor executes one post-generation phase.
type PostProcessor interface {
	Name() ProcessorName
	Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
}

// SceneVoiceover is a single scene-voiceover outcome from VoiceoverProcessor.
type SceneVoiceover struct {
	SceneIndex int
	Language   string
	Status     string
	Link       string
	LocalPath  string
	DurationMs int64

	Timing *scriptpkg.VoiceoverTimingBinding

	TimingArtifact *capabilityaudio.SpeechTimingArtifact `json:"-"`
}

// SceneImage is a single scene-image outcome from ImageProcessor.
type SceneImage struct {
	Index       int
	Text        string
	URL         string
	DriveFileID string
}

// SceneImageDriveLink returns the public or Drive URL for a scene image.
func SceneImageDriveLink(img SceneImage) string {
	url := strings.TrimSpace(img.URL)
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	if img.DriveFileID != "" {
		return urlutil.DriveFileURL(img.DriveFileID)
	}
	return url
}

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	VisualPlans         []mediamemory.SceneVisualPlan
	VisualAssignments   []mediadomain.VisualAssignment `json:"visual_assignments,omitempty"`
	Entities            *scriptpkg.EntityResult
	VidRushSegments     []scriptpkg.VidRushSegmentResult
	Metadata            []scriptpkg.VideoMetadata
	Voiceovers          []SceneVoiceover
	SceneImages         []SceneImage
	ScriptID            int64
	AlreadyPersisted    bool
	Changed             bool                         `json:"changed,omitempty"`
	SynthesizedScenes   []scriptpkg.SpecScene        `json:"synthesized_scenes,omitempty"`
	Warnings            []string                     `json:"warnings,omitempty"`
	TranslatedText      string                       `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput    `json:"translated_specscene,omitempty"`
	OriginalText        string                       `json:"original_text,omitempty"`
	OriginalSpecScene   scriptpkg.SpecSceneOutput    `json:"original_specscene,omitempty"`
	EffectiveLanguage   string                       `json:"effective_language,omitempty"`
	StageProgress       map[string]job.StageProgress `json:"stage_progress,omitempty"`
	UpdatedSpecScene    scriptpkg.SpecSceneOutput    `json:"updated_specscene,omitempty"`
	SpecSceneChanged    bool                         `json:"-"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	if r.Changed {
		return false
	}
	if r.Entities != nil {
		if len(r.Entities.Persons) > 0 || len(r.Entities.Places) > 0 || len(r.Entities.Concepts) > 0 {
			return false
		}
	}
	if len(r.VisualAssignments) > 0 ||
		len(r.VidRushSegments) > 0 ||
		len(r.Metadata) > 0 ||
		len(r.Voiceovers) > 0 ||
		len(r.SceneImages) > 0 {
		return false
	}
	if r.ScriptID > 0 || r.AlreadyPersisted {
		return false
	}
	if len(r.SynthesizedScenes) > 0 || len(r.UpdatedSpecScene.Scenes) > 0 {
		return false
	}
	if strings.TrimSpace(r.TranslatedText) != "" || len(r.TranslatedSpecScene.Scenes) > 0 {
		return false
	}
	return true
}

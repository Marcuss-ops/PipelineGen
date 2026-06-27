// Package adapters — postprocessor_types.go defines the shared types
// needed by the postprocessor adapters (processor_clip_bindings.go,
// processor_document.go, etc.) that implement the PostProcessor interface.
//
// These types mirror the canonical definitions in package scripts
// (postprocessor_registry.go) so the adapters can compile without
// importing the scripts package (avoiding potential circular deps).
package adapters

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Per-stage result types ───────────────────────────────────────────

// SceneVoiceover is a single scene-voiceover outcome from VoiceoverProcessor.
type SceneVoiceover struct {
	SceneIndex int
	Status     string
	Link       string
	LocalPath  string
}

// SceneImage is a single scene-image outcome from ImageProcessor.
type SceneImage struct {
	Index int
	Text  string
	URL   string
}

// ── Shared postprocessor types ───────────────────────────────────────

// ProcessorPolicy classifies a postprocessor's failure mode.
type ProcessorPolicy string

const (
	ProcessorRequired   ProcessorPolicy = "required"
	ProcessorBestEffort ProcessorPolicy = "best_effort"
)

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text           string
	WordCount      int
	SpecScene      scriptpkg.SpecSceneOutput
	ModelUsed      string
	CacheStatus    string
	SourceTrace    *scriptpkg.ClipEvidence
	PriorArtifacts map[string]PostProcessResult
}

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	Entities         *scriptpkg.EntityResult
	Metadata         []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	SceneImages      []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string `json:"warnings,omitempty"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	if r.Entities != nil {
		if len(r.Entities.Persons) > 0 || len(r.Entities.Places) > 0 || len(r.Entities.Concepts) > 0 {
			return false
		}
	}
	if len(r.Metadata) > 0 {
		return false
	}
	if len(r.Voiceovers) > 0 {
		return false
	}
	if len(r.SceneImages) > 0 {
		return false
	}
	if r.DocLink != "" || r.DocID != "" {
		return false
	}
	if r.ScriptID > 0 || r.AlreadyPersisted {
		return false
	}
	return true
}

// PipelineResult aggregates the postprocessor outputs.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	Scenes           []SceneImage
	SceneImages      []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string
}

// PostProcessorRegistry is a stub used by adapters.
// The canonical registry lives in package scripts.
type PostProcessorRegistry struct{}

// NewPostProcessorRegistry creates an empty stub registry.
func NewPostProcessorRegistry() *PostProcessorRegistry {
	return &PostProcessorRegistry{}
}

// ValidateRequested is a stub that always returns nil (no-op).
func (r *PostProcessorRegistry) ValidateRequested(names []string) error {
	return nil
}

// Run is a stub that returns an empty PipelineResult.
func (r *PostProcessorRegistry) Run(ctx interface{}, plan interface{}, input ProcessInput) (*PipelineResult, error) {
	return &PipelineResult{}, nil
}

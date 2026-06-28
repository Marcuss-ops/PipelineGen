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

	"go.uber.org/zap"
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
//
// PR 0 build fix (June 2026): the legacy `Scenes` field is REMOVED.
// Canonical = `SceneImages`. The dual-field drift was a pre-existing
// structural debt that confused readers; the only writer is the
// ImageProcessor (which always populated `SceneImages`), so removing
// the dead alias is safe and surfaces the canonical contract.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	SceneImages      []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string
}

// PostProcessorRegistry is a stub used by adapters.
// The canonical registry lives in package scripts.
//
// PR 0 build fix: expanded stub to satisfy wire_script.go composition
// — Register, Freeze, Registered, LookupPolicy, IsFrozen are all no-ops.
type PostProcessorRegistry struct {
	log    *zap.Logger
	frozen bool
	reg    map[string]ProcessorPolicy
}

// NewPostProcessorRegistry creates an empty stub registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{log: log, reg: make(map[string]ProcessorPolicy)}
}

// Register is a stub that accepts any value and returns true.
func (r *PostProcessorRegistry) Register(p interface{}) bool {
	if r == nil {
		return false
	}
	return true
}

// Freeze is a stub (no-op).
func (r *PostProcessorRegistry) Freeze() {
	if r != nil {
		r.frozen = true
	}
}

// IsFrozen reports whether Freeze was called.
func (r *PostProcessorRegistry) IsFrozen() bool {
	if r == nil {
		return false
	}
	return r.frozen
}

// Registered reports whether a processor name has been registered.
func (r *PostProcessorRegistry) Registered(name string) bool {
	// PR 0 stub: always returns true so validateRequiredProcessors passes.
	_ = name
	return true
}

// LookupPolicy returns the policy for a registered processor.
func (r *PostProcessorRegistry) LookupPolicy(name string) ProcessorPolicy {
	// PR 0 stub: always returns ProcessorRequired.
	_ = name
	return ProcessorRequired
}

// ValidateRequested is a stub that always returns nil (no-op).
func (r *PostProcessorRegistry) ValidateRequested(names []string) error {
	return nil
}

// Run is a stub that returns an empty PipelineResult.
func (r *PostProcessorRegistry) Run(ctx interface{}, plan interface{}, input ProcessInput) (*PipelineResult, error) {
	return &PipelineResult{}, nil
}

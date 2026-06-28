// Package adapters — postprocessor_types.go defines the shared value
// types needed by the postprocessor adapters (processor_clip_bindings.go,
// processor_document.go, processor_persistence.go, etc.) when they
// implement PostProcessor defined in package scripts.
//
// Audit #6 (Phase 4, June 2026):
//
//   - The prior PostProcessorRegistry stub (a placeholder that existed
//     only to break the inheritance build cycle in PR 0) was REMOVED.
//     Production composition now uses the canonical registry in
//     internal/application/scripts via the *scripts.PostProcessorRegistry
//     type. See scripts.PostProcessorRegistry for Run /
//     ValidateRequested / Freeze / Registered / LookupPolicy.
//
//   - The prior adapters.ProcessorPolicy shadow + its constants
//     (ProcessorRequired / ProcessorBestEffort) was REMOVED. Every
//     processor's Policy() method now returns scripts.ProcessorPolicy
//     (canonical). Without this consolidation, Go's distinct-type-per-
//     declaration rule would block the assignment
//     `r.policies[name] = policy` inside scripts.PostProcessorRegistry
//     .Register (where r.policies is `map[string]scripts.ProcessorPolicy`).
//     Future processors MUST follow suit — declare
//     `func (p *X) Policy(...) scripts.ProcessorPolicy { ... }` with
//     `return scripts.ProcessorRequired` / `scripts.ProcessorBestEffort`.
//
// Adapters still consume the following value types directly, so they
// remain in this package:
//
//   - SceneVoiceover / SceneImage : per-stage aggregate result slices
//     that processor_voiceover.go + processor_clip_bindings.go build.
//   - ProcessInput                 : the typed envelope passed to
//     every PostProcessor.Process(...) call. Processor implementations
//     decompose this into canonical fields (Text, WordCount, SpecScene,
//     CacheStatus, ...).
//   - PostProcessResult + IsEmpty  : the per-processor output struct
//     that flows into the canonical PipelineResult aggregate.
//   - PipelineResult               : the canonical aggregate the use
//     case consumes after a registry.Run.
//
// Read references for the canonical owners:
//
//   - scripts.PostProcessorRegistry (canonical registry)
//   - scripts.PostProcessorPolicy (canonical Policy enum)
//   - scripts.ProcessorRequired / scripts.ProcessorBestEffort
//   - scripts.PostProcessResult (the canonical envelope shape)
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

// ProcessorPolicy classifies a postprocessor's failure mode for the
// canonical pipeline audit refactor (PR ref
// fix/required-postprocessor-semantics, June 2026).
//
// Three explicit values replace the legacy two-value enum:
//
//   - RequiredAll (`required_all`)        — every processor with this
//     policy MUST succeed. Any single failure (error, nil result, empty
//     output, missing-registered) is a hard error: Run returns a
//     typed error wrapping ErrPostprocessFailed. Audit #2 closed:
//     previously the gate fired only when ALL required-class names
//     failed simultaneously, so a failing persistence + succeeding
//     document silently returned a "successful" job despite missing the
//     canonical ScriptID deliverable.
//
//   - RequiredAtLeastOne (`required_at_least_one`) — at least one
//     processor with this policy MUST succeed. Multiple processors
//     sharing this policy on the same plan tolerate individual
//     failures as long as one succeeds. Hard error only when every
//     processor in the cohort failed.
//
//   - BestEffort (`best_effort`) — never blocks. Failures surface in
//     PipelineResult.Warnings so the operator sees the observation
//     without aborting the script generation.
//
// Legacy aliases (ProcessorRequired / ProcessorBestEffort) are kept
// so pre-PR 3 caller code that referenced ProcessorRequired under
// the old "all-or-nothing" semantic still compiles. The canonical
// enum values are the three above; future PRs migrate callers off
// the aliases.
type ProcessorPolicy string

const (
	// RequiredAll is the canonical "any failure = hard error" policy.
	RequiredAll ProcessorPolicy = "required_all"

	// RequiredAtLeastOne is the canonical "at least one succeeds" policy.
	RequiredAtLeastOne ProcessorPolicy = "required_at_least_one"

	// BestEffort is the canonical "never blocks" policy.
	BestEffort ProcessorPolicy = "best_effort"

	// Legacy aliases — pre-PR ref callers that referenced the
	// two-value enum. RequiredAll collapses what was previously
	// `ProcessorRequired` under the new "any failure" semantic.
	ProcessorRequired   ProcessorPolicy = RequiredAll
	ProcessorBestEffort ProcessorPolicy = BestEffort
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
	Warnings         []string `json:"warnings,omitempty"`
}

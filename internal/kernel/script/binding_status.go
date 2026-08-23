// Package script — binding_status.go defines the canonical enums
// for image binding and voiceover binding statuses. LLM / processor
// authors MUST emit values from these sets; unknown values are
// rejected by ValidateAndEnrichSpecScene.
//
// PR 6 (June 2026): replaces the pre-PR-6 free-form `string` status
// fields on ImageBinding.Status and VoiceoverBinding.Status with
// typed enums. Empty status is accepted for image bindings (in
// flight before the postprocessor emits a terminal value); voiceover
// bindings are postprocessor-only and require a terminal value.
package script

import "fmt"

// ImageBindingStatus enumerates the canonical lifecycle states for an
// AI-generated scene image binding. The image postprocessor
// transitions Pending → Generated or Pending → Failed.
type ImageBindingStatus string

const (
	// ImageStatusPending — image generation has been queued but
	// has not produced a URL/path yet. Pre-processor state.
	ImageStatusPending ImageBindingStatus = "pending"
	// ImageStatusGenerated — image generation completed; URL +
	// LocalPath are populated. Terminal-success state.
	ImageStatusGenerated ImageBindingStatus = "generated"
	// ImageStatusFailed — image generation failed. Terminal-error
	// state.
	ImageStatusFailed ImageBindingStatus = "failed"
)

// Valid reports whether s is a known ImageBindingStatus.
func (s ImageBindingStatus) Valid() bool {
	switch s {
	case ImageStatusPending, ImageStatusGenerated, ImageStatusFailed:
		return true
	}
	return false
}

// IsTerminal reports true for terminal states (Generated or Failed).
func (s ImageBindingStatus) IsTerminal() bool {
	return s == ImageStatusGenerated || s == ImageStatusFailed
}

// VoiceoverBindingStatus enumerates the canonical lifecycle states
// for a generated voiceover audio binding. Voiceover bindings are
// postprocessor-only — the LLM never emits them.
type VoiceoverBindingStatus string

const (
	// VoiceoverStatusPending — voiceover generation queued.
	VoiceoverStatusPending VoiceoverBindingStatus = "pending"
	// VoiceoverStatusCompleted — voiceover finished; Link +
	// LocalPath are populated. Terminal-success state.
	VoiceoverStatusCompleted VoiceoverBindingStatus = "completed"
	// VoiceoverStatusFailed — voiceover generation failed.
	// Terminal-error state.
	VoiceoverStatusFailed VoiceoverBindingStatus = "failed"
	// VoiceoverStatusSkipped — caller explicitly disabled
	// voiceover via ToggleDisabled. Terminal-neutral state.
	VoiceoverStatusSkipped VoiceoverBindingStatus = "skipped"
)

// Valid reports whether s is a known VoiceoverBindingStatus.
func (s VoiceoverBindingStatus) Valid() bool {
	switch s {
	case VoiceoverStatusPending, VoiceoverStatusCompleted,
		VoiceoverStatusFailed, VoiceoverStatusSkipped:
		return true
	}
	return false
}

// IsTerminal is true for any state other than Pending.
func (s VoiceoverBindingStatus) IsTerminal() bool {
	return s != VoiceoverStatusPending && s != ""
}

// ErrSpecSceneInvalid is the sentinel for any specscene validation
// or enrichment failure (model-invented clip, missing binding on
// kind=clip, invalid temporal range, unknown status, ...).
// Workers and dashboards use errors.Is(err, ErrSpecSceneInvalid)
// to surface typed details via *SpecSceneValidationError.
var ErrSpecSceneInvalid = fmt.Errorf("script: specscene invalid")

// SpecSceneValidationError carries the structured details behind
// ErrSpecSceneInvalid.
//
// PR 6 (June 2026): introduced so the specscene validator can
// surface per-scene failures (which scene index, which rule) to the
// operator dashboard rather than as a generic error. The pre-PR-6
// behaviour surfaced a single "engine failed" string and made it
// impossible to debug which scene the model invented.
type SpecSceneValidationError struct {
	ItemID          string
	BadSceneIndices []int
	SceneKindBad    []SceneKind // SceneKind values that triggered rejects
	Reason          string      // human-readable summary
	Inner           error       // optional underlying cause
}

func (e *SpecSceneValidationError) Error() string {
	if e == nil {
		return ErrSpecSceneInvalid.Error()
	}
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s", ErrSpecSceneInvalid.Error(), e.Reason)
	}
	return fmt.Sprintf("%s: item=%s bad_scene_indices=%v bad_scene_kinds=%v",
		ErrSpecSceneInvalid.Error(), e.ItemID, e.BadSceneIndices, e.SceneKindBad)
}

func (e *SpecSceneValidationError) Unwrap() error {
	if e == nil || e.Inner == nil {
		return ErrSpecSceneInvalid
	}
	return e.Inner
}

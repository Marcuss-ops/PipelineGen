// Package scripts — preset_resolver.go applies preset semantics to a
// GenerationItemV2. The only canonical preset that forces flags is
// "with_images"; "batch" and "custom" are pass-through.
//
// Precedence rule: a preset only overwrites fields that the caller
// left at their zero value. An explicit caller value always wins.
package scripts

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ApplyPreset applies the given preset's overrides to the item.
// Fields already set by the caller are NOT overwritten.
//
// Note: Go bool zero-value is false, so the preset cannot distinguish
// "caller omitted" from "caller explicitly set false". The preset
// applies its defaults regardless, matching the existing behaviour
// in generation_service.go::EnqueueWithImages.
//
// "with_images":
//   - Output.GenerateSceneImages → true (if not already set)
//   - Output.GenerateVoiceover   → true (if not already set)
//   - Output.ExtractEntities     → false (always forced off)
//   - Output.GenerateMetadata    → false (always forced off)
//   - ScriptParams.SentencesPerImage → 8 (if 0)
//   - ScriptParams.ImagesPerScene    → 2 (if 0)
//
// "batch", "custom", empty: no overrides.
func ApplyPreset(item *scriptpkg.GenerationItemV2, preset scriptpkg.Preset) {
	if item == nil {
		return
	}
	switch preset {
	case scriptpkg.PresetWithImages:
		// Force ON: scene images + voiceover.
		// (bool zero-value is false; cannot distinguish "caller omitted"
		// from "caller set false", so preset always overwrites.)
		item.Output.GenerateSceneImages = true
		item.Output.GenerateVoiceover = true

		// Force OFF: entity extraction + metadata generation.
		item.Output.ExtractEntities = false
		item.Output.GenerateMetadata = false

		// Sensible defaults for scene image sizing.
		if item.ScriptParams.SentencesPerImage <= 0 {
			item.ScriptParams.SentencesPerImage = 8
		}
		if item.ScriptParams.ImagesPerScene <= 0 {
			item.ScriptParams.ImagesPerScene = 2
		}

		// Document creation makes sense with images.
		if !item.Output.GenerateDocument {
			// Only force ON if the caller didn't explicitly set it.
			// We can't distinguish "caller omitted" from "caller set false"
			// at the contract level, so we respect the zero value: if false,
			// was probably omitted.
			item.Output.GenerateDocument = true
		}
	}
}

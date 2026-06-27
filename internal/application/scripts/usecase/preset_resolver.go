// Package scripts — preset_resolver.go applies preset semantics to
// a GenerationItemV2. The only canonical preset that adds flags is
// "with_images"; "batch" and "custom" are pass-through.
//
// PR 8 (June 2026) precedence contract:
//
//	caller explicit > preset > config > safety
//
// PR 8 (June 2026) preset semantics update:
//
//	"with_images" previously forced ON  scene_images + voiceover + document
//	                     and forced OFF entities + metadata. That was
//	                     wrong: it overwrote caller intent and
//	                     silently re-shaped bodies that had no image
//	//                     concept.
//
//	"with_images" now ONLY enables scene_images. Voiceover, document,
//	entities, and metadata are caller-controlled (with the standard
//	caller > preset > config > safety precedence via the existing
//	OutputSpec bool contract).
//
//	A caller who wants voiceover alongside with_images sets
//	GenerateVoiceover explicitly; a caller who wants to disable
//	images sets GenerateSceneImages=false and the preset no longer
//	fights them.
//
//	Note: Go bool zero-value is false, so the preset cannot
//	distinguish "caller omitted" from "caller set false". The
//	preset applies its sole override (scene_images) only when
//	the caller left it at zero, matching the per-field flag
//	contract documented in generation_normalizer.go.
package usecase

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ApplyPreset applies the given preset's overrides to the item.
// Fields already set by the caller are NOT overwritten.
//
// "with_images":
//   - Output.GenerateSceneImages -> true (if caller didn't set)
//   - ScriptParams.SentencesPerImage -> 8 (if 0)
//   - ScriptParams.ImagesPerScene   -> 2 (if 0)
//
// "with_images" does NOT modify:
//   - GenerateVoiceover
//   - GenerateDocument
//   - ExtractEntities
//   - GenerateMetadata
//   - VoiceoverGroup/Folder
//
// "batch", "custom", empty: pass-through (no overrides).
func ApplyPreset(item *scriptpkg.GenerationItemV2, preset scriptpkg.Preset) {
	if item == nil {
		return
	}
	switch preset {
	case scriptpkg.PresetWithImages:
		// Force ON: scene images (sole preset responsibility).
		item.Output.GenerateSceneImages = true

		// Sensible defaults for scene image sizing. PR 8: images
		// get smaller and faster without inflating into voiceover
		// territory.
		if item.ScriptParams.SentencesPerImage <= 0 {
			item.ScriptParams.SentencesPerImage = 8
		}
		if item.ScriptParams.ImagesPerScene <= 0 {
			item.ScriptParams.ImagesPerScene = 2
		}

		// PR 8: respect caller for voiceover, document, entities,
		// metadata. The preset resists the urge to "helpfully"
		// chain these on.
		// Caller > preset precedence (already enforced by leaving
		// these fields alone); the normalizer fills safety
		// defaults if the caller left them at zero.

	default:
		// batch, custom, empty -> pass-through.
	}
}

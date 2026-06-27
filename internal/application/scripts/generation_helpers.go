// Package scripts — generation_helpers.go holds shared conversion
// helpers used by both the use case (generate_one_usecase.go) and
// processors (processor_entities.go, processor_metadata.go).
//
// These helpers bridge the legacy GenerationSpec with the new
// ResolvedGenerationPlan during the migration window. Remove
// after PR 12 (CONTRACT) when GenerationSpec is deleted.
package scripts

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// legacySpecFromPlan builds a legacy GenerationSpec from a
// ResolvedGenerationPlan for backward compatibility with code
// that still consumes *GenerationSpec (PostGenFunc callback,
// old pipeline consumers).
//
// Deprecated: remove after PR 12 when GenerationSpec is deleted
// and all consumers migrate to ResolvedGenerationPlan.
func legacySpecFromPlan(plan scriptpkg.ResolvedGenerationPlan) *scriptpkg.GenerationSpec {
	spec := &scriptpkg.GenerationSpec{
		Title:             plan.Title,
		Language:          plan.Language,
		Tone:              plan.Tone,
		Model:             plan.Model,
		TargetWords:       plan.TargetWords,
		Duration:          plan.Duration,
		MinWords:          plan.MinWords,
		SentencesPerImage: plan.SentencesPerImage,
		ImagesPerScene:    plan.ImagesPerScene,
		Style:             plan.Style,
		Guidelines:        plan.Guidelines,
		MaxChars:          plan.MaxChars,
		OutputFmt:         plan.OutputFmt,
		SaveToDB:          plan.SaveToDB,
		ExtractEntities:     plan.HasPostprocessor("entities"),
		GenerateMetadata:    plan.HasPostprocessor("metadata"),
		GenerateVoiceover:   plan.HasPostprocessor("voiceover"),
		GenerateSceneImages: plan.HasPostprocessor("images"),
		PromptVersion:       plan.PromptVersion,
		EditorPromptVersion: plan.EditorPromptVersion,
		QAPromptVersion:     plan.QAPromptVersion,
		ForceRefresh:        plan.ForceRefresh,
		Languages:           plan.Languages,
	}
	if plan.ClipEvidence != nil {
		spec.ClipIDs = plan.ClipEvidence.ClipIDs
		spec.NumClips = plan.ClipEvidence.ClipCount
	}
	return spec
}

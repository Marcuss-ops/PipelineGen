// Package voiceover — command.go (PR-VOICEOVER-COMMAND-EXTRACT, June 2026).
//
// GenerateVoiceoversCommand is the canonical singular Command that
// GenerateVoiceoversUseCase.Execute consumes. It supersedes the
// positional signature of `Service.Generate(ctx, text, language, filename)`
// with a typed surface so the use case logic can fan out N languages
// without losing configuration context per request.
//
// Field ownership:
//   - Text, Languages, FilenameTemplate, VoiceOverrides, Strategy,
//     RemoveSilence, Parallelism, Metadata are Command-specific (the
//     use case owns them).
//   - Destination is *DestinationRequest (existing wire-shape from
//     types.go — reuse over re-declaration so the canonical wire
//     envelope stays the single source of truth).
//   - VoiceOverrides is the per-language override map keyed by
//     BCP-47 code; if a key is missing the TTSProvider falls back
//     to its default voice.
//
// The use case Execute path calls cmd.Validate() FIRST so 400-equivalent
// paths short-circuit before any port invocation (mirrors the
// path-traversal-rejection-before-field-access pattern pinned by
// TestGenerateBatch_RejectsPathTraversalPayload at stages.go).
package voiceover

import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

// GenerateVoiceoversCommand is the canonical singular request to the
// use case. Mirrors the canonical wire-shape from BatchRequest
// (types.go) but uses asset.PipelineStrategy for type-safe strategy
// (the three canonical values are verify / skip / replace; the
// canonical normaliser is asset.NormalizeStrategy).
type GenerateVoiceoversCommand struct {
	// Text is the source voiceover text. Required (Validate enforces
	// non-empty).
	Text string

	// Languages is the list of BCP-47 codes to generate. Required
	// non-empty; each code must pass LanguageCodeValid.
	Languages []string

	// FilenameTemplate is the optional template; falls back to
	// "{slug}_{lang}.mp3" via BuildFilename if empty.
	FilenameTemplate string

	// VoiceOverrides is the per-language voice override map keyed by
	// BCP-47 code. nil-safe (use case falls through to TTSProvider
	// default voice when the key is missing).
	VoiceOverrides map[string]string

	// Destination is the optional routing payload. nil means "use the
	// config-level voiceover folder" (deferred to composition root;
	// reserved for Block 4 / composition root wiring).
	Destination *DestinationRequest

	// Strategy is the pipeline strategy (verify / skip / replace).
	// asset.NormalizeStrategy (lifecycle_core.go:118) is the canonical
	// normaliser invoked by the use case at the boundary.
	Strategy asset.PipelineStrategy

	// RemoveSilence toggles AudioPostProcessor.PostProcess after TTS.
	RemoveSilence bool

	// Parallelism is the requested fan-out concurrency. Clamped by the
	// use case to min(requested, MaxParallelism, len(Languages)).
	Parallelism int

	// Metadata is the user-supplied meta overlay that flows into the
	// row's metadata column (process_metadata_test.go pins the
	// collision-drop contract from mergeUserMetadata).
	Metadata map[string]any
}

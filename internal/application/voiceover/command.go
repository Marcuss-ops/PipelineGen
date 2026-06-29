// Package voiceover — command.go (PR-VOICEOVER-COMMAND-EXTRACT, June 2026).
//
// GenerateVoiceoversCommand is the canonical singular Command that
// GenerateVoiceoversUseCase.Execute consumes. It supersedes the
// positional signature of `Service.Generate(ctx, text, language, filename)`
// with a typed surface so the use case logic can fan out N languages
// without losing configuration context per request.
//
// JSON tags are explicit per AGENTS.md Pattern 6 (snake_case wire
// keys + omitempty on optionals) so the HTTP handler can round-trip
// `cmd` directly into job.Payload WITHOUT an intermediate map. The
// canonical wire shape mirrors BatchRequest.PayloadMap()'s snake_case
// keys for back-compat callers; future endpoints inheriting this shape
// get field-level stability for free.
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
//
// JSON tags pin the canonical wire shape mirror of
// BatchRequest.PayloadMap() — the same snake_case keys used in the
// legacy BatchRequest wire path. Round-tripping this struct into
// job.Payload and back via json.Unmarshal reproduces an identical
// GenerateVoiceoversCommand on the consumer side (jobs/generate_handler.go).
type GenerateVoiceoversCommand struct {
	// Text is the source voiceover text. Required (Validate enforces
	// non-empty).
	Text string `json:"text"`

	// Languages is the list of BCP-47 codes to generate. Required
	// non-empty; each code must pass LanguageCodeValid.
	Languages []string `json:"languages"`

	// FilenameTemplate is the optional template; falls back to
	// "{slug}_{lang}.mp3" via BuildFilename if empty.
	FilenameTemplate string `json:"filename_template,omitempty"`

	// VoiceOverrides is the per-language voice override map keyed by
	// BCP-47 code. nil-safe (use case falls through to TTSProvider
	// default voice when the key is missing).
	VoiceOverrides map[string]string `json:"voice_overrides,omitempty"`

	// Destination is the optional routing payload. nil means "use the
	// config-level voiceover folder" (deferred to composition root;
	// reserved for Block 4 / composition root wiring).
	Destination *DestinationRequest `json:"destination,omitempty"`

	// Strategy is the pipeline strategy (verify / skip / replace).
	// asset.NormalizeStrategy (lifecycle_core.go:118) is the canonical
	// normaliser invoked by the use case at the boundary.
	Strategy asset.PipelineStrategy `json:"strategy,omitempty"`

	// RemoveSilence toggles AudioPostProcessor.PostProcess after TTS.
	// Wire semantics: JSON `null` / omitted maps to false (no
	// post-processing). Explicit `true` enables post-processing. The
	// legacy BatchRequest.RemoveSilence used `*bool` to distinguish
	// "not set" from "false"; the canonical Command uses plain `bool`
	// because the per-language worker (processOneLanguage) gates on
	// truthy only — the three-state nuance is not worth a typed
	// dangling-pointer for the new async use case. Valid JSON
	// round-trips cleanly via omitempty.
	RemoveSilence bool `json:"remove_silence,omitempty"`

	// Parallelism is the requested fan-out concurrency. Clamped by the
	// use case to min(requested, MaxParallelism, len(Languages)).
	Parallelism int `json:"parallelism,omitempty"`

	// Metadata is the user-supplied meta overlay that flows into the
	// row's metadata column (process_metadata_test.go pins the
	// collision-drop contract from mergeUserMetadata).
	Metadata map[string]any `json:"metadata,omitempty"`
}

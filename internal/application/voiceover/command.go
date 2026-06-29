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

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

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

// ── Per-language child command (PR-VOICEOVER-PARENT-CHILD-FANOUT, P0.3) ──

// GenerateVoiceoverItemCommand is the canonical payload of the per-language
// child job (job type == job.TypeVoiceoverGenerateItem). It carries ONLY
// the data the child worker needs to execute ONE (lang, voice) pair:
// the shared batch-level constants (Text, Destination, Strategy,
// Metadata, FilenameTemplate) and the per-pair derived values
// (Language, Voice, Filename, TextHash, RequestID, ParentJobID).
//
// Why a separate command instead of reusing GenerateVoiceoversCommand
// with Languages=[lang]: the child worker should NEVER iterate over
// Languages — the fan-out is the parent's responsibility. Carrying the
// whole batch struct would leak goroutine-style fan-out semantics into
// the child surface.
//
// Field ownership:
//   - ParentJobID, RequestID: batch-level IDs the child threads into
//     its DB row + outbox event so the aggregator can find the parent.
//   - Language, Voice, Filename: per-pair derived (FanoutUseCase
//     computes them via buildCommandFilename + voice overrides).
//   - Text, TextHash: batch-level constants.
//   - Destination: the OUTPUT DestinationRequest — the child re-resolves
//     it independently so a child retry that survives a transient
//     resolver hiccup does not need the parent to re-run.
//   - Strategy, RemoveSilence, Metadata: pass-through from the parent.
type GenerateVoiceoverItemCommand struct {
	// ParentJobID is the dispatcher-assigned job.ID of the parent
	// voiceover.generate job. Aggregator in Commit 2 reads this from
	// outbox events to identify siblings.
	ParentJobID string `json:"parent_job_id"`

	// RequestID is the per-batch identifier (buildRequestID shape:
	// vo_<timestamp>_<6-hex-suffix>). Stable across all siblings so
	// cross-language audit can query by request_id and get the full set.
	RequestID string `json:"request_id"`

	// Text is the source voiceover text (required, validated).
	Text string `json:"text"`

	// Language is the BCP-47 code for THIS child. Exactly one.
	Language string `json:"language"`

	// Voice is the BCP-47-tied voice override for THIS child.
	// Empty falls through to TTSProvider default voice.
	Voice string `json:"voice,omitempty"`

	// Filename is the pre-computed sanitised filename (same shape
	// the parent would have computed via buildCommandFilename).
	Filename string `json:"filename"`

	// TextHash is the per-batch SHA-256(Text). Pre-computed by the
	// parent so every child writes the same text_hash into its row.
	TextHash string `json:"text_hash"`

	// Destination is the per-batch routing payload (nullable: if absent,
	// the child uses the canonical config-level voiceover folder).
	Destination *DestinationRequest `json:"destination,omitempty"`

	// Strategy is the pipeline strategy (verify / skip / replace).
	Strategy asset.PipelineStrategy `json:"strategy,omitempty"`

	// RemoveSilence toggles audio post-processing for THIS child.
	RemoveSilence bool `json:"remove_silence,omitempty"`

	// Metadata is the per-batch user-supplied meta overlay that flows
	// into the row's metadata column.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Validate runs the canonical validation gate ONCE at the child use case
// boundary. Mirrors GenerateVoiceoversCommand.Validate but enforces
// single-language semantics.
func (c *GenerateVoiceoverItemCommand) Validate() error {
	if c == nil {
		return fmt.Errorf("nil GenerateVoiceoverItemCommand")
	}
	if strings.TrimSpace(c.Text) == "" {
		return fmt.Errorf("text: must be non-empty")
	}
	if !LanguageCodeValid(c.Language) {
		return fmt.Errorf("language: invalid code %q (only alphanumeric + hyphens allowed)", c.Language)
	}
	if strings.TrimSpace(c.Filename) == "" {
		return fmt.Errorf("filename: must be non-empty (pre-computed by FanoutUseCase)")
	}
	if strings.TrimSpace(c.ParentJobID) == "" {
		return fmt.Errorf("parent_job_id: must be non-empty (dispatcher-assigned at parent enqueue)")
	}
	if strings.TrimSpace(c.RequestID) == "" {
		return fmt.Errorf("request_id: must be non-empty (built by FanoutUseCase)")
	}
	if c.Destination != nil {
		if vErr := c.Destination.Validate(); vErr != nil {
			return fmt.Errorf("destination: %w", vErr)
		}
	}
	return nil
}

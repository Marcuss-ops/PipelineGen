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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VoiceoverItem is the canonical per-item shape: one (text, language,
// voice, filename) row that becomes one child voiceover.generate_item
// job at fan-out. Items are independent — text may vary across items,
// voice and filename override the per-item defaults.
//
// Step 5 (P0.3 items-model recovery, June 2026): the legacy P0.2
// wire-shape collapsed items[] into a flat GenerateVoiceoversCommand
// (1 Text + N Languages[] + VoiceOverrides map + FilenameTemplate).
// The collapse dropped information when two items shared a language
// (later-wins VoiceOverrides, later-wins FilenameTemplate) and
// forced identical texts across items. The Item-ROM model restores
// per-item independence so mixed-text requests, two voices for the
// same language, and per-item filenames are all first-class.
//
// FASE 2 (July 2026): Required field added. When true, the child's
// outcome gates the parent terminal state — a REQUIRED-failed child
// short-circuits the domain StateMachine to FailedTerminal in
// Transition() rule ①. Optional-failed children are tolerated
// when at least one child succeeded (Compute() FASE 1 semantics).
//
// JSON tags mirror the canonical API wire shape so unmarshalling a
// request payload directly into a VoiceoverItem list produces the
// same field set the HTTP handler binds to GenerateVoiceoversRequest.
type VoiceoverItem struct {
	// Text is the source voiceover text for THIS item. Required
	// (Validate enforces non-empty per item).
	Text string `json:"text"`

	// Language is the BCP-47 code for THIS item. Required; must pass
	// LanguageCodeValid (alphanumeric + hyphens only). Typed
	// (Language) per PR-VO-TYPED-PRIMITIVES (July 2026) — JSON wire
	// shape is byte-equivalent with the pre-refactor string field.
	Language Language `json:"language"`

	// Voice is the per-item voice override. Empty falls through to
	// TTSProvider's default voice (nil-safe at every layer).
	Voice string `json:"voice,omitempty"`

	// Filename is the per-item sanitised filename. Empty triggers the
	// {slug}_{lang}_{unix}.mp3 fallback in the fan-out (buildItemFilename).
	Filename string `json:"filename,omitempty"`

	// Required gates the parent terminal state (FASE 2, July 2026).
	// When true, this child's failure short-circuits the parent to
	// FailedTerminal; when false, the failure is tolerated if at
	// least one other child succeeded (Compute() FASE 1 semantics).
	Required bool `json:"required,omitempty"`
}

// GenerateVoiceoversCommand is the canonical singular request to the
// use case. Step 5 drops the P0.2 collapsed shape (1 Text + N Languages
// + VoiceOverrides map + FilenameTemplate) in favour of an Items[]
// model where each VoiceoverItem carries its own text/language/voice/
// filename. The use case fans out one child GenerateVoiceoverItemCommand
// per item — the per-language iteration at the parent layer is gone.
//
// Field ownership after Step 5:
//   - Items: per-row work (one child job per item).
//   - Destination: batch-level routing (shared across all items).
//   - Strategy, RemoveSilence, Parallelism, Metadata: batch-level
//     configuration (shared across all items).
//   - RequestID: batch-level correlation (parent → every child).
//
// Round-tripping the struct into job.Payload and back via json.Unmarshal
// reproduces an identical GenerateVoiceoversCommand on the consumer
// side (jobs/generate_handler.go).
type GenerateVoiceoversCommand struct {
	// Items is the list of independent (text, language, voice, filename)
	// rows. Each item becomes one voiceover.generate_item child job at
	// fan-out (micro-commit #2 of PR-VOICEOVER-PARENT-CHILD-FANOUT).
	// Items MAY have different texts, voices with the same language, or
	// per-item filenames — no P0.2 shared-text invariant.
	Items []VoiceoverItem `json:"items"`

	// Destination is the optional routing payload. nil means "use the
	// config-level voiceover folder" (deferred to composition root;
	// reserved for Block 4 / composition root wiring). When non-nil,
	// the same Destination applies to EVERY item in the batch.
	Destination *DestinationRequest `json:"destination,omitempty"`

	// Strategy is the pipeline strategy (verify / skip / replace).
	// asset.NormalizeStrategy (lifecycle_core.go:118) is the canonical
	// normaliser invoked by the use case at the boundary. The same
	// Strategy is applied to every item in the batch.
	Strategy asset.PipelineStrategy `json:"strategy,omitempty"`

	// RemoveSilence toggles AudioPostProcessor.PostProcess after TTS.
	// Wire semantics: JSON `null` / omitted maps to false (no
	// post-processing). Explicit `true` enables post-processing for
	// every item in the batch.
	RemoveSilence bool `json:"remove_silence,omitempty"`

	// Timing is the canonical voiceover timing policy for the whole
	// batch. nil means the canonical defaults apply
	// (best_effort / word / [json]) — timing capture is never
	// implicitly mandatory.
	Timing *audio.TimingRequest `json:"voiceover_timing,omitempty"`

	// Parallelism is the requested fan-out concurrency. Clamped by the
	// use case to min(requested, MaxParallelism, len(Items)).
	Parallelism int `json:"parallelism,omitempty"`

	// Metadata is the user-supplied meta overlay that flows into the
	// row's metadata column (process_metadata_test.go pins the
	// collision-drop contract from mergeUserMetadata). Applied to every
	// child row in the batch — per-item metadata overlays are P1 scope.
	Metadata map[string]any `json:"metadata,omitempty"`

	// RequestID is the stable correlation identifier threaded from the
	// API caller through the parent job and into every child. When
	// non-empty, FanoutVoiceoversUseCase.Execute uses this value
	// instead of generating a new BuildRequestID(). Populated by
	// GenerateJobHandler.HandleJob from j.CorrelationID.
	RequestID string `json:"request_id,omitempty"`

	// Project is the canonical semantic project identifier for the
	// voiceover publish (canonical surface for the P12 wave's
	// ThreadingCampaign, 2026-07-08). Threaded from the API request
	// down through the fanout loop into every
	// GenerateVoiceoverItemCommand → ProcessSegmentCommand → adapter →
	// Publisher's VoiceoverPath so voiceovers land in
	// `{project}/{language}/` Drive subdirs via delivery.Publisher.
	//
	// Propagation chain (godlike/06 SSOT one canonical owner per fact):
	//   1. GenerateVoiceoversRequest.Project (internal/capabilities/assets/voiceover/types.go)
	//   2. GenerateVoiceoversCommand.Project (this struct)
	//   3. GenerateVoiceoverItemCommand.Project (per-item, fan-out copy)
	//   4. delivery.Publisher.Publish (read by VoiceoverPath builder)
	//
	// godlike/07 minimum-blast-radius: empty Project falls through the
	// adapter to the pre-P12 FolderID or the canonical voiceover ID
	// (graceful degradation — existing callers do not break).
	Project string `json:"project,omitempty"`
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
// FASE 2 (July 2026): Required field added. Propagated from the
// VoiceoverItem.Required flag set by the API caller. The aggregator
// reads this field from the child job's payload to feed the domain
// StateMachine's REQUIRED-failed short-circuit (Transition() rule ①).
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
//   - Strategy, RemoveSilence, Metadata, Required: pass-through from the parent.
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
	// Typed (Language) per PR-VO-TYPED-PRIMITIVES.
	Language Language `json:"language"`

	// Voice is the BCP-47-tied voice override for THIS child.
	// Empty falls through to TTSProvider default voice.
	Voice string `json:"voice,omitempty"`

	// Filename is the pre-computed sanitised filename (same shape
	// the parent would have computed via buildCommandFilename).
	Filename string `json:"filename"`

	// TextHash is the per-batch SHA-256(Text). Pre-computed by the
	// parent so every child writes the same text_hash into its row.
	// Typed (TextHash) per PR-VO-TYPED-PRIMITIVES.
	TextHash TextHash `json:"text_hash"`

	// Destination is the per-batch routing payload (nullable: if absent,
	// the child uses the canonical config-level voiceover folder).
	Destination *DestinationRequest `json:"destination,omitempty"`

	// Strategy is the pipeline strategy (verify / skip / replace).
	Strategy asset.PipelineStrategy `json:"strategy,omitempty"`

	// RemoveSilence toggles audio post-processing for THIS child.
	RemoveSilence bool `json:"remove_silence,omitempty"`

	// Timing is the voiceover timing policy for THIS child, copied
	// from the parent batch. nil means the canonical defaults apply.
	Timing *audio.TimingRequest `json:"voiceover_timing,omitempty"`

	// Moments are the optional LLM-produced annotation queries (kind +
	// value) to anchor onto the canonical word timing. Only the scene
	// fanout path populates this (from SceneAnnotations); the batch path
	// has no annotations and leaves it nil.
	Moments []audio.MomentQuery `json:"moments,omitempty"`

	// Metadata is the per-batch user-supplied meta overlay that flows
	// into the row's metadata column.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Required gates the parent terminal state (FASE 2, July 2026).
	// Propagated from VoiceoverItem.Required by the fan-out.
	// Read by the aggregator from the child job's payload to feed
	// the domain StateMachine Transition() rule ①.
	Required bool `json:"required,omitempty"`

	// Project is the canonical semantic project identifier for the
	// voiceover publish (PR-P12-VOICEOVER-SEMANTIC-FIELDS, July 2026).
	// Threaded from the API request (or, for internal callers, from
	// the per-call context) down to ProcessSegmentCommand → the
	// adapter → the Publisher's VoiceoverPath. When empty, the
	// adapter falls back to the pre-PR-12 FolderID (legacy) or
	// the canonical voiceover ID (graceful degradation).
	Project string `json:"project,omitempty"`
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
	// PR-VO-TYPED-PRIMITIVES (July 2026): route through the canonical
	// LanguageCodeValid gate (preserves the existing test surface).
	if !LanguageCodeValid(string(c.Language)) {
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
	if c.Timing != nil {
		// Normalize first so an empty policy (all-zero slots) resolves to
		// the canonical defaults instead of failing; caller-explicit
		// invalid values still surface.
		if vErr := c.Timing.Normalized().Validate(); vErr != nil {
			return fmt.Errorf("voiceover_timing: %w", vErr)
		}
	}
	return nil
}

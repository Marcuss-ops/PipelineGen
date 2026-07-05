// Package script — generation_errors.go defines the typed error
// contract for the unified script-generation pipeline. Each sentinel
// wraps a typed struct so callers can use errors.Is for coarse
// classification and errors.As for structured details.
package script

import (
	"errors"
	"fmt"
)

// ── Sentinels ───────────────────────────────────────────────────────

// ErrNoSource means the GenerationItemV2 has an empty or invalid
// SourceSpec (no type set, or type=text with no topic).
var ErrNoSource = errors.New("generation: no source specified")

// ErrSourceResolutionFailed means the source resolver could not
// produce a ResolvedSource (e.g. catalog search returned zero clips
// and the caller did not opt into text-only fallback).
var ErrSourceResolutionFailed = errors.New("generation: source resolution failed")

// ErrVoiceoverResolveFailed means the voiceover_group → folder
// resolution failed at the pre-BuildPlan step (Phase 4 of
// GenerateOneUseCase.Execute). Distinct from ErrSourceResolutionFailed
// — that one spans clip-search (catalog / Qdrant / drive) failures
// while this one covers Drive folder-routing failures of the voiceover
// destination. PR-ERROR-SURFACING commit-5 (2026-07-04): introduced as
// the canonical phase sentinel for the voiceover_resolve escape path
// so handlers / dashboards can fan out cleanly by failure domain
// instead of over-broadening ErrSourceResolutionFailed to cover both.
// godlike/06 SSOT (one canonical owner per fact): this sentinel lives
// ONLY at internal/domain/script/generation_errors.go.
var ErrVoiceoverResolveFailed = errors.New("generation: voiceover resolve failed")

// ErrGenerationFailed wraps any failure from the engine itself
// (Ollama call, memory-gate error, script too short).
var ErrGenerationFailed = errors.New("generation: engine failed")

// ErrPostprocessFailed wraps any failure from a postprocessor
// (document creation, image generation, voiceover, entity extraction).
var ErrPostprocessFailed = errors.New("generation: postprocess failed")

// ErrPlanInvalid means the ResolvedGenerationPlan failed validation
// after normalization (e.g. no source text, impossible sizing
// constraints, conflicting flags).
var ErrPlanInvalid = errors.New("generation: plan invalid")

// ErrEntityExtractorUnavailable means the script postprocessor tried
// to invoke an EntityExtractor but the composition root wired a noop
// adapter (no real backend was registered). PR-NOOP-ADAPTERS-PURGE
// (2026-07-04): godlike/07 fail-closed posture — the noop MUST surface
// this typed sentinel instead of returning a silently-empty
// EntityResult. Dual-%w in compat_adapters.go preserves both
// ErrPostprocessFailed (coarse classification) AND this sentinel
// (fine-grained diagnostic) for errors.Is walkers.
var ErrEntityExtractorUnavailable = errors.New("generation: entity extractor unavailable")

// ErrMetadataGeneratorUnavailable means the script postprocessor tried
// to invoke a MetadataGenerator but the composition root wired a noop
// adapter (no real backend was registered). PR-NOOP-ADAPTERS-PURGE
// (2026-07-04): godlike/07 fail-closed posture — the noop MUST surface
// this typed sentinel instead of returning a silently-nil
// VideoMetadata slice. Dual-%w in compat_adapters.go preserves both
// ErrPostprocessFailed (coarse classification) AND this sentinel
// (fine-grained diagnostic) for errors.Is walkers.
var ErrMetadataGeneratorUnavailable = errors.New("generation: metadata generator unavailable")

// ErrScriptGenerationFailed is the canonical umbrella sentinel for any
// unrecoverable failure of the unified script.generate pipeline.
//
// PR-ERROR-SURFACING (2026-07-04): used as the godlike/07 typed-error
// root when /api/jobs/{id}/full surfaces a non-empty top-level `error`
// field for a script.generate job that reached FAILED. The full chain
// is preserved via fmt.Errorf with %w so callers can errors.Is walk
// from the public error string:
//
//	errors.Is(err, scriptpkg.ErrScriptGenerationFailed) → umbrella match
//	errors.Is(err, scriptpkg.ErrPostprocessFailed)       → postprocess sibling
//	errors.Is(err, scriptpkg.ErrEntityExtractorUnavailable) or ErrMetadataGeneratorUnavailable → fine-grained
//
// godlike/06 SSOT (one canonical owner per fact): this sentinel lives
// ONLY at internal/domain/script/generation_errors.go. No other
// package declares a duplicate. Future typed-sentinel additions for
// the script generation capability MUST be sibling entries here.
var ErrScriptGenerationFailed = errors.New("generation: script generation failed")

// ── Typed structs ───────────────────────────────────────────────────

// NoSourceError carries the structured reason behind ErrNoSource.
type NoSourceError struct {
	ItemID string
	Reason string
}

func (e *NoSourceError) Error() string {
	if e == nil {
		return ErrNoSource.Error()
	}
	return fmt.Sprintf("%s: item=%q reason=%s", ErrNoSource.Error(), e.ItemID, e.Reason)
}

func (e *NoSourceError) Unwrap() error { return ErrNoSource }

// SourceResolutionError carries the structured details behind
// ErrSourceResolutionFailed.
type SourceResolutionError struct {
	SourceType  SourceType
	Query       string
	ResultCount int
	Inner       error
}

func (e *SourceResolutionError) Error() string {
	if e == nil {
		return ErrSourceResolutionFailed.Error()
	}
	msg := fmt.Sprintf("%s: type=%s query=%q results=%d",
		ErrSourceResolutionFailed.Error(), e.SourceType, e.Query, e.ResultCount)
	if e.Inner != nil {
		msg += ": " + e.Inner.Error()
	}
	return msg
}

func (e *SourceResolutionError) Unwrap() error { return ErrSourceResolutionFailed }

// GenerationError carries the structured details behind
// ErrGenerationFailed.
type GenerationError struct {
	ItemID string
	Phase  string // "engine", "memory_gate", "ollama"
	Inner  error
}

func (e *GenerationError) Error() string {
	if e == nil {
		return ErrGenerationFailed.Error()
	}
	msg := fmt.Sprintf("%s: item=%q phase=%s", ErrGenerationFailed.Error(), e.ItemID, e.Phase)
	if e.Inner != nil {
		msg += ": " + e.Inner.Error()
	}
	return msg
}

func (e *GenerationError) Unwrap() error { return ErrGenerationFailed }

// PostprocessError carries the structured details behind
// ErrPostprocessFailed.
type PostprocessError struct {
	ItemID    string
	Processor string // "document", "images", "voiceover", "entities", "metadata"
	Inner     error
}

func (e *PostprocessError) Error() string {
	if e == nil {
		return ErrPostprocessFailed.Error()
	}
	msg := fmt.Sprintf("%s: item=%q processor=%s", ErrPostprocessFailed.Error(), e.ItemID, e.Processor)
	if e.Inner != nil {
		msg += ": " + e.Inner.Error()
	}
	return msg
}

func (e *PostprocessError) Unwrap() error { return ErrPostprocessFailed }

// PlanInvalidError carries the structured details behind
// ErrPlanInvalid.
type PlanInvalidError struct {
	ItemID  string
	Details []string
}

func (e *PlanInvalidError) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrPlanInvalid.Error()
	}
	msg := fmt.Sprintf("%s: item=%q", ErrPlanInvalid.Error(), e.ItemID)
	for _, d := range e.Details {
		msg += "; " + d
	}
	return msg
}

func (e *PlanInvalidError) Unwrap() error { return ErrPlanInvalid }

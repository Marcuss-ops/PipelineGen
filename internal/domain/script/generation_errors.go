// Package script — generation_errors.go defines the typed error
// contract for the unified script-generation pipeline. Each sentinel
// wraps a typed struct so callers can use errors.Is for coarse
// classification and errors.As for structured details.
package script

import (
	"errors"
	"fmt"
	"strings"
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

// ErrClipNativePlanningFailed means a clip-native source could not be
// planned without falling back to prose and the fallback policy was
// strict (or unset). Introduced for the strict clip-native pipeline
// (July 2026).
var ErrClipNativePlanningFailed = errors.New("generation: clip-native planning failed")

// ErrQualityGateFailed means the editorial quality gate rejected the
// generated script. The failure details are carried by
// QualityGateError.
var ErrQualityGateFailed = errors.New("generation: editorial quality gate failed")

// ── P0 error classification (July 2026) ────────────────────────────
//
// The following sentinels implement the canonical HTTP error
// classification contract per the verdetto:
//
//	400  payload or source.type not valid
//	409  idempotency conflict with different payload
//	422  scene formally valid but not processable
//	502  Gemma or Docs invalid response
//	503  provider not configured or temporarily unavailable
//	504  timeout provider

// ErrUnprocessable means the request is formally valid (valid JSON,
// valid envelope schema) but the engine cannot process it — e.g.
// a specific source type is not supported by the configured provider.
// Maps to HTTP 422 Unprocessable Entity.
var ErrUnprocessable = errors.New("generation: unprocessable entity")

// ErrProviderBadResponse means an upstream provider (Ollama/Gemma,
// Google Docs, Drive) returned an invalid or unexpected response.
// Maps to HTTP 502 Bad Gateway.
var ErrProviderBadResponse = errors.New("generation: provider returned invalid response")

// ErrProviderTimeout means an upstream provider timed out.
// Maps to HTTP 504 Gateway Timeout.
var ErrProviderTimeout = errors.New("generation: provider timeout")

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

// PayloadValidationError carries a stable machine-readable code and
// optional structured context for a payload validation failure.
// It is the canonical surface for 400 Bad Request responses on
// POST /api/script/generate.
type PayloadValidationError struct {
	// Code is the stable error code (e.g. "SOURCE_TEXT_TOO_LARGE").
	Code string
	// Message is a human-readable description.
	Message string
	// Stage is the pipeline stage where the validation failed.
	Stage string
	// Retryable is false for validation errors (the caller must fix
	// the payload).
	Retryable bool
	// Extra carries structured context (e.g. actual_chars, max_chars).
	Extra map[string]any
}

func (e *PayloadValidationError) Error() string {
	if e == nil {
		return ErrPlanInvalid.Error()
	}
	return fmt.Sprintf("%s: code=%s message=%s", ErrPlanInvalid.Error(), e.Code, e.Message)
}

func (e *PayloadValidationError) Unwrap() error { return ErrPlanInvalid }

// ClipNativePlanningError carries the structured details behind
// ErrClipNativePlanningFailed. It surfaces when a clip-native source
// cannot produce a scene-per-clip plan and the fallback policy does
// not allow prose fallback.
type ClipNativePlanningError struct {
	Code    string
	ItemID  string
	Policy  string
	Reason  string
	Details []string
}

func (e *ClipNativePlanningError) Error() string {
	if e == nil {
		return ErrClipNativePlanningFailed.Error()
	}
	code := e.Code
	if code == "" {
		code = "CLIP_NATIVE_PLANNING_FAILED"
	}
	msg := fmt.Sprintf("%s code=%s item=%q policy=%q reason=%q",
		ErrClipNativePlanningFailed.Error(), code, e.ItemID, e.Policy, e.Reason)
	if len(e.Details) > 0 {
		msg += "; " + strings.Join(e.Details, "; ")
	}
	return msg
}

func (e *ClipNativePlanningError) Unwrap() error { return ErrClipNativePlanningFailed }

// IsClipNativePlanningFailed reports whether err is (or wraps)
// ErrClipNativePlanningFailed.
func IsClipNativePlanningFailed(err error) bool {
	return errors.Is(err, ErrClipNativePlanningFailed)
}

// QualityGateError carries the structured details behind
// ErrQualityGateFailed. It surfaces which editorial checks failed
// and the computed quality metrics.
type QualityGateError struct {
	Code    string
	ItemID  string
	Reasons []string
	Quality GenerationQuality
}

func (e *QualityGateError) Error() string {
	if e == nil {
		return ErrQualityGateFailed.Error()
	}
	code := e.Code
	if code == "" {
		code = "QUALITY_GATE_FAILED"
	}
	msg := fmt.Sprintf("%s: code=%s item=%q", ErrQualityGateFailed.Error(), code, e.ItemID)
	if len(e.Reasons) > 0 {
		msg += "; " + strings.Join(e.Reasons, "; ")
	}
	return msg
}

func (e *QualityGateError) Unwrap() error { return ErrQualityGateFailed }

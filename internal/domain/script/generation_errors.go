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

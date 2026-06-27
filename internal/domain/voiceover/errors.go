// Package voiceover — errors.go defines the canonical sentinel errors
// and structured error types for voiceover generation.
//
// PR 1 (June 2026): each error carries a distinct type so callers can
// use errors.Is / errors.As for precise handling. Replaces ad-hoc
// fmt.Errorf strings scattered across the legacy service.
package voiceover

import "fmt"

// ── Sentinels ────────────────────────────────────────────────────────────

var (
	// ErrTextRequired is returned when GenerateVoiceoverCommand.Text is empty.
	ErrTextRequired = &ValidationError{Field: "text", Reason: "must not be empty"}

	// ErrLocaleRequired is returned when GenerateVoiceoverCommand.Locale is empty.
	ErrLocaleRequired = &ValidationError{Field: "locale", Reason: "must not be empty"}

	// ErrDestinationInvalid is returned when the destination folder ID is
	// syntactically invalid (empty after trim).
	ErrDestinationInvalid = &ValidationError{Field: "destination.folder_id", Reason: "must not be empty when destination is set"}
)

// ── Structured errors ────────────────────────────────────────────────────

// ValidationError is a field-level validation failure.
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("voiceover: %s %s", e.Field, e.Reason)
}

// LocaleNotSupportedError is returned when the requested locale is not
// in the voice registry.
type LocaleNotSupportedError struct {
	Locale Locale `json:"locale"`
}

func (e *LocaleNotSupportedError) Error() string {
	return fmt.Sprintf("voiceover: locale %q is not supported", e.Locale.String())
}

// VoiceNotAvailableError is returned when the requested voice is not
// available for the given locale.
type VoiceNotAvailableError struct {
	Locale Locale `json:"locale"`
	Voice  string `json:"voice"`
}

func (e *VoiceNotAvailableError) Error() string {
	return fmt.Sprintf("voiceover: voice %q is not available for locale %q", e.Voice, e.Locale.String())
}

// GenerationError wraps a TTS provider failure.
type GenerationError struct {
	Locale  Locale `json:"locale"`
	Voice   string `json:"voice"`
	Message string `json:"message"`
}

func (e *GenerationError) Error() string {
	return fmt.Sprintf("voiceover: TTS generation failed for locale=%q voice=%q: %s", e.Locale.String(), e.Voice, e.Message)
}

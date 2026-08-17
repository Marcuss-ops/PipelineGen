// Package texttracks — errors.go: typed error sentinels for
// TextTrackMaterializer and the asset.text.materialize job handler.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// godlike/07 typed-error contract: every error this package produces
// is a typed sentinel that callers can match via errors.Is. The
// job handler in jobs.go classifies these sentinels into TERMINAL
// (no retry benefit) vs RETRYABLE (broker default policy) per the
// canonical broker job-handler pattern.
package texttracks

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrTranslationFailed is the canonical terminal sentinel for a
// translation round-trip that returned an error from the underlying
// TranslationPort. A wrapped error is the verbatim TranslationPort
// error; callers can extract details via errors.Unwrap.
//
// godlike/07 fail-closed: the materializer MUST NOT silently fall
// back to an empty translated text on translation failure.
type ErrTranslationFailed struct {
	AssetID       string
	TargetLang    string
	TextKind      asset.TextTrackKind
	Cause         error
	AttemptedText string
}

func (e *ErrTranslationFailed) Error() string {
	return fmt.Sprintf(
		"texttracks: translation failed (asset=%s kind=%s target=%s): %v",
		e.AssetID, e.TextKind, e.TargetLang, e.Cause,
	)
}

func (e *ErrTranslationFailed) Unwrap() error { return e.Cause }

func (e *ErrTranslationFailed) Is(target error) bool {
	var t *ErrTranslationFailed
	return errors.As(target, &t)
}

// ErrUnsupportedLanguage is the terminal sentinel for a target
// language that is NOT in MultilingualConfig.MaterializeLanguages.
type ErrUnsupportedLanguage struct {
	AssetID        string
	TargetLanguage string
	Allowed        []string
}

func (e *ErrUnsupportedLanguage) Error() string {
	return fmt.Sprintf(
		"texttracks: language %q is not in materialize_languages (allowed=%v, asset=%s)",
		e.TargetLanguage, e.Allowed, e.AssetID,
	)
}

func (e *ErrUnsupportedLanguage) Is(target error) bool {
	var t *ErrUnsupportedLanguage
	return errors.As(target, &t)
}

// ErrTrackNotReady is the terminal sentinel for a materialization
// request whose source track is in a non-READY status.
type ErrTrackNotReady struct {
	AssetID            string
	SourceLanguage     string
	TextKind           asset.TextTrackKind
	CurrentStatus      asset.TextTrackStatus
	AvailableStatuses  []asset.TextTrackStatus
	AvailableLanguages []string
}

func (e *ErrTrackNotReady) Error() string {
	return fmt.Sprintf(
		"texttracks: source track not READY (asset=%s kind=%s source=%s status=%s ready_languages=%v)",
		e.AssetID, e.TextKind, e.SourceLanguage, e.CurrentStatus, e.AvailableLanguages,
	)
}

func (e *ErrTrackNotReady) Is(target error) bool {
	var t *ErrTrackNotReady
	return errors.As(target, &t)
}

// ErrNoSourceTrack is the terminal sentinel for an asset that has
// no source track at all.
type ErrNoSourceTrack struct {
	AssetID        string
	SourceLanguage string
	TextKind       asset.TextTrackKind
}

func (e *ErrNoSourceTrack) Error() string {
	return fmt.Sprintf(
		"texttracks: no source track for asset=%s kind=%s source=%s",
		e.AssetID, e.TextKind, e.SourceLanguage,
	)
}

func (e *ErrNoSourceTrack) Is(target error) bool {
	var t *ErrNoSourceTrack
	return errors.As(target, &t)
}

// ErrInvalidMaterializeRequest is the terminal sentinel for a
// materialization request whose payload is structurally invalid.
type ErrInvalidMaterializeRequest struct {
	Field  string
	Reason string
}

func (e *ErrInvalidMaterializeRequest) Error() string {
	return fmt.Sprintf("texttracks: invalid materialize request (field=%s): %s", e.Field, e.Reason)
}

func (e *ErrInvalidMaterializeRequest) Is(target error) bool {
	var t *ErrInvalidMaterializeRequest
	return errors.As(target, &t)
}

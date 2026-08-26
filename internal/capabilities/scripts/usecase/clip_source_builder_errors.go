// Package usecase — clip_source_builder_errors.go owns the typed
// error surface for the ClipSourceBuilder video-pipeline cutover
// to TextTrackReader (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4, July 2026).
//
// godlike/07 typed-error contract: every error type below carries
// enough structured payload for the operator dashboard to surface
// the failure mode WITHOUT requiring the caller to re-query the
// repository. The struct form (ErrTextTrackNotReady) is used when
// the payload is structured (asset + requested + available + kind);
// the sentinel form is used for flat-failure modes (the other 4).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// TextTrackReader error surface. Callers (handler tests, downstream
// post-processors) MUST NOT define their own sentinels; they import
// from this file and pattern-match on Error() output.
package usecase

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ErrTextTrackNotReady is the canonical typed error returned by
// ClipSourceBuilder.BuildClipContext when the caller requested a
// specific (asset, language, kind) text track and the
// TextTrackReader could not produce a READY row.
//
// The struct form is the godlike/07 contract for typed errors
// with structured payloads. The four fields together let the
// caller render an actionable message ("asset X has no READY
// Italian transcript; available languages: en, es") without a
// second round-trip to the repository.
type ErrTextTrackNotReady struct {
	// AssetID is the canonical asset identifier (media_assets.id
	// or drive_file_id; whichever was supplied as the clip ID).
	AssetID string

	// RequestedLanguage is the BCP-47 code the caller asked for.
	RequestedLanguage string

	// AvailableLanguages is the sorted set of language codes for
	// which a READY track exists for the (AssetID, MissingKind)
	// pair. Populated by ListReadyLanguages; nil when the
	// repository call failed (caller can render "unknown" then).
	AvailableLanguages []string

	// MissingKind is the TextTrackKind the lookup was for. The
	// canonical Fase 4 caller passes TextTrackTranscript
	// (clip builders always want a transcript).
	MissingKind detail.TextTrackKind
}

// Error implements the error interface. The string form is
// human-readable and stable (godlike/07): the operator dashboard
// pattern-matches on substrings, NOT on the exact format. Adding
// new fields to the struct is non-breaking; changing the
// substring tokens below IS breaking.
func (e *ErrTextTrackNotReady) Error() string {
	avail := "<none>"
	if len(e.AvailableLanguages) > 0 {
		// Stable, sorted rendering so dashboards sort messages
		// consistently. Use a defensive copy so a concurrent
		// mutation of the slice can't tear the message.
		sorted := append([]string(nil), e.AvailableLanguages...)
		sort.Strings(sorted)
		avail = strings.Join(sorted, ",")
	}
	return fmt.Sprintf(
		"text track not ready: asset_id=%q requested_language=%q missing_kind=%q available_languages=%s",
		e.AssetID, e.RequestedLanguage, string(e.MissingKind), avail,
	)
}

// Is implements the errors.Is contract so callers can pattern-match
// with `errors.Is(err, &ErrTextTrackNotReady{})` regardless of the
// concrete struct value. The match is a TYPE check, not a value
// check (a zero-value ErrTextTrackNotReady{} matches any populated
// instance) — this is the canonical godlike/07 typed-error
// pattern: type-discriminated, value-agnostic.
func (e *ErrTextTrackNotReady) Is(target error) bool {
	_, ok := target.(*ErrTextTrackNotReady)
	return ok
}

// ErrTextTrackTranslationFailed is the canonical sentinel for
// failures during the translation leg of the text-track pipeline.
// Today the video pipeline NEVER invokes translation (per Fase 4
// contract: the pipeline never calls TranslationPort); this
// sentinel is reserved for the future /api/script/generate flow
// that DOES translate (or for the backfill CLI's translation leg).
//
// godlike/07 NO-FAKE-AVAILABILITY: a returned ErrTextTrackTranslationFailed
// is a hard failure (no silent no-op). Callers MUST surface the
// error to the operator dashboard, not absorb it.
var ErrTextTrackTranslationFailed = errors.New("text track: translation failed")

// ErrTextTrackSourceChanged is the canonical sentinel for the
// "source text changed → old translations invalidated" case.
// Returned by the text-track writer when a re-derive of an
// existing track changes the source_text_hash and forces a
// re-translation of all derived language rows.
//
// Per the test 8 contract: "source_text_hash cambiato → vecchie
// traduzioni invalidati + ritradotti". The pipeline (writer) is
// responsible for emitting this sentinel + invalidating the
// dependent rows; the reader (ClipSourceBuilder) is responsible
// for observing the new READY row on the next call.
var ErrTextTrackSourceChanged = errors.New("text track: source text changed (re-derive required)")

// ErrTextTrackLanguageUnsupported is the canonical sentinel for
// "this (asset, language) pair is not in the configured
// materialize-languages list". Returned by the backfill CLI
// (Fase 5) and by the writer when a caller asks for a
// language not in the operator-configured allow-list. The video
// pipeline surfaces this BEFORE the call to TextTrackReader.
//
// godlike/07 fail-closed: the pipeline MUST NOT silently
// no-op a language-unsupported request. Return the sentinel
// + the unsupported language code in the operator message.
var ErrTextTrackLanguageUnsupported = errors.New("text track: language not in materialize list")

// ErrTextTrackTimedCuesMissing is the canonical sentinel for
// "the READY track exists but carries no timed cues and the
// caller required them". Returned by callers that pass a
// require-cues flag to the reader (e.g. the video re-rendering
// pipeline that needs segment-level timing).
//
// Today ClipSourceBuilder does NOT require cues (it only needs
// the flat transcript text for the prompt); this sentinel is
// reserved for future consumers that do. The Fase 4 contract
// pins the typed error so the future caller can pattern-match
// without re-deriving the contract.
var ErrTextTrackTimedCuesMissing = errors.New("text track: ready track found but no timed cues")

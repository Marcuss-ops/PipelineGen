// Package usecase — clip_language_association.go owns the check that every
// clip bound to a script is CORRECTLY ASSOCIATED with the script's language,
// plus the runtime remediation when it is not.
//
// Godlike/06 SSOT (one canonical owner per fact): this file is the SOLE owner
// of the "is this clip's text-track set associated with the language this
// script asked for?" decision. Before it, that answer was implicit in the
// shape of *ErrTextTrackNotReady (a nil AvailableLanguages meant "nothing at
// all"; a populated one meant "something, but not what you asked for"), which
// forced every caller to re-derive the distinction from carry data.
//
// The check has two halves:
//
//  1. ClassifyClipLanguageAssociation — PURE and deterministic: given the
//     requested language and the READY languages the reader reported, it
//     returns ready | missing | mismatch. No I/O, no clock, no RNG.
//
//  2. resolveTranscriptChecked — the orchestration: resolve the transcript;
//     on a not-READY track, ASK the canonical materializer to create and
//     PERSIST the missing text track at runtime (ports.ClipTextTrackEnsurer),
//     then re-resolve. Only when the track is still not READY does it return
//     the typed *ClipLanguageAssociationError.
//
// Godlike/07 NO-FAKE-AVAILABILITY: an unwired ensurer is NOT a pass. It keeps
// the historical fail-closed behaviour and reports Status=missing/mismatch, so
// "we could not materialize" can never be mistaken for "the clip is fine".
package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// ClipLanguageAssociationStatus is the canonical classification of the
// relationship between a script's requested language and the READY text tracks
// of a clip bound to that script.
type ClipLanguageAssociationStatus string

const (
	// ClipLanguageReady means a READY text track exists in the requested
	// language. It is never carried by an error; it is the classifier's
	// success value.
	ClipLanguageReady ClipLanguageAssociationStatus = "ready"

	// ClipLanguageMissing means the clip has NO READY text track in ANY
	// language: the subtitles were never created. Remediation is
	// acquisition + materialization (create and save at runtime).
	ClipLanguageMissing ClipLanguageAssociationStatus = "missing"

	// ClipLanguageMismatch means the clip HAS READY text tracks, but not in
	// the requested language: the clip is associated with the wrong language
	// for this script. Remediation is translation of the existing source text
	// (create and save at runtime).
	ClipLanguageMismatch ClipLanguageAssociationStatus = "mismatch"

	// ClipLanguageMaterializeFailed means the runtime materializer ran and
	// still did not produce a READY track in the requested language. The run
	// MUST fail closed: the script cannot be generated from a transcript that
	// does not exist.
	ClipLanguageMaterializeFailed ClipLanguageAssociationStatus = "materialize_failed"
)

// ClassifyClipLanguageAssociation is the pure, deterministic check.
//
// requested is the language the script asked for; available is the set of
// languages that DO have a READY track for the clip (as reported by
// ListReadyLanguages — nil/empty means "none"). An empty requested language is
// treated as ready: the caller applies its configured default downstream, and
// this check must not invent a mismatch for a request that never named one.
func ClassifyClipLanguageAssociation(requested string, available []string) ClipLanguageAssociationStatus {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return ClipLanguageReady
	}
	found := false
	for _, lang := range available {
		if strings.EqualFold(strings.TrimSpace(lang), requested) {
			found = true
			break
		}
	}
	if found {
		return ClipLanguageReady
	}
	if len(available) == 0 {
		return ClipLanguageMissing
	}
	return ClipLanguageMismatch
}

// ClipLanguageAssociationError is the canonical typed error for "the clip
// bound to this script is not correctly associated with the script language".
//
// It wraps the underlying *ErrTextTrackNotReady as its Unwrap target so every
// existing probe keeps working: errors.As(err, &notReady) and
// errors.Is(err, &ErrTextTrackNotReady{}) both succeed through the chain. The
// added value is the explicit Status plus the runtime-materialization failure,
// which the pre-existing error surface could not express.
type ClipLanguageAssociationError struct {
	// AssetID is the clip whose text tracks were inspected.
	AssetID string

	// RequestedLanguage is the language the script asked for.
	RequestedLanguage string

	// AvailableLanguages is the sorted set of languages that DO have a READY
	// track. Empty means the clip has no READY transcript at all.
	AvailableLanguages []string

	// Status is the classification. It is never ClipLanguageReady.
	Status ClipLanguageAssociationStatus

	// TrackError is the underlying not-READY error. It is always populated and
	// is the Unwrap target.
	TrackError *ErrTextTrackNotReady

	// EnsureError is the runtime-materialization failure, populated only when
	// Status is ClipLanguageMaterializeFailed.
	EnsureError error
}

// Error implements the error interface. The string form keeps the stable
// tokens of ErrTextTrackNotReady ("text track not ready", "asset_id=",
// "requested_language=", "available_languages=") so existing operator
// dashboards continue to match, and adds the association status.
func (e *ClipLanguageAssociationError) Error() string {
	if e == nil {
		return "clip language association: <nil>"
	}
	avail := "<none>"
	if len(e.AvailableLanguages) > 0 {
		sorted := append([]string(nil), e.AvailableLanguages...)
		sort.Strings(sorted)
		avail = strings.Join(sorted, ",")
	}
	msg := fmt.Sprintf(
		"clip language association %q: asset_id=%q requested_language=%q available_languages=%s text track not ready",
		string(e.Status), e.AssetID, e.RequestedLanguage, avail,
	)
	if e.EnsureError != nil {
		msg += ": runtime materialization failed: " + e.EnsureError.Error()
	}
	return msg
}

// Unwrap exposes the underlying not-READY error so the existing typed-error
// contract (errors.As / errors.Is against *ErrTextTrackNotReady) is preserved
// rather than replaced.
func (e *ClipLanguageAssociationError) Unwrap() error { return e.TrackError }

// Is implements the errors.Is contract: a probe against the same struct type
// matches any instance, and a probe against *ErrTextTrackNotReady is delegated
// to the wrapped error.
func (e *ClipLanguageAssociationError) Is(target error) bool {
	if _, ok := target.(*ClipLanguageAssociationError); ok {
		return true
	}
	return e != nil && e.TrackError != nil && e.TrackError.Is(target)
}

// newClipLanguageAssociationError builds the typed error from the not-READY
// error the reader produced, reusing its carry data (asset, requested
// language, available languages) so the message stays actionable.
func newClipLanguageAssociationError(notReady *ErrTextTrackNotReady, status ClipLanguageAssociationStatus, ensureErr error) *ClipLanguageAssociationError {
	out := &ClipLanguageAssociationError{
		Status:      status,
		TrackError:  notReady,
		EnsureError: ensureErr,
	}
	if notReady != nil {
		out.AssetID = notReady.AssetID
		out.RequestedLanguage = notReady.RequestedLanguage
		out.AvailableLanguages = append([]string(nil), notReady.AvailableLanguages...)
	}
	return out
}

// resolveTranscriptChecked is the canonical "resolve the clip's transcript,
// creating and persisting it at runtime when the language association is
// wrong" entry point. It replaces the raw resolveTranscript call at the clip
// resolution site; resolveTranscript itself stays the (unchanged) sole reader
// of asset_text_tracks.
//
// Flow:
//  1. resolveTranscript — a READY track short-circuits, no materialization.
//  2. not READY + no ensurer wired ⇒ typed *ClipLanguageAssociationError with
//     Status=missing|mismatch (historical fail-closed behaviour preserved).
//  3. not READY + ensurer wired ⇒ EnsureReadyTextTrack (create + persist),
//     then re-resolve.
//  4. still not READY after the ensure ⇒ typed error with
//     Status=materialize_failed.
func (c *ClipSourceBuilder) resolveTranscriptChecked(
	ctx context.Context,
	assetID string,
	language string,
	clip *asset.Asset,
) (string, *detail.TextTrack, error) {
	transcript, track, err := c.resolveTranscript(ctx, assetID, language, clip)
	if err == nil {
		return transcript, track, nil
	}

	var notReady *ErrTextTrackNotReady
	if !errors.As(err, &notReady) {
		// A non-track failure (e.g. the reader is a dependency-shape error we
		// did not classify) keeps its original error: this seam must not
		// reclassify failures it does not own.
		return "", nil, err
	}

	requested := strings.TrimSpace(language)
	status := ClassifyClipLanguageAssociation(requested, notReady.AvailableLanguages)
	if c.transcriptEnsurer == nil {
		c.logLanguageAssociation(assetID, requested, status)
		return "", nil, newClipLanguageAssociationError(notReady, status, nil)
	}

	if ensureErr := c.transcriptEnsurer.EnsureReadyTextTrack(ctx, assetID, requested, detail.TextTrackTranscript); ensureErr != nil {
		if c.log != nil {
			c.log.Warn("clip source builder: runtime text track materialization failed",
				zap.String("asset_id", assetID),
				zap.String("language", requested),
				zap.Error(ensureErr))
		}
		return "", nil, newClipLanguageAssociationError(notReady, ClipLanguageMaterializeFailed, ensureErr)
	}

	transcript, track, err = c.resolveTranscript(ctx, assetID, language, clip)
	if err == nil {
		if c.log != nil {
			c.log.Info("clip source builder: text track materialized at runtime",
				zap.String("asset_id", assetID),
				zap.String("language", requested),
				zap.String("previous_status", string(status)))
		}
		return transcript, track, nil
	}

	var stillNotReady *ErrTextTrackNotReady
	if !errors.As(err, &stillNotReady) {
		return "", nil, err
	}
	return "", nil, newClipLanguageAssociationError(stillNotReady, ClipLanguageMaterializeFailed, err)
}

// logLanguageAssociation emits the canonical WARN for a failed association
// check when runtime materialization is not available in this composition.
func (c *ClipSourceBuilder) logLanguageAssociation(assetID, requested string, status ClipLanguageAssociationStatus) {
	if c.log == nil {
		return
	}
	c.log.Warn("clip source builder: script language is not associated with the clip and no runtime materializer is wired",
		zap.String("asset_id", assetID),
		zap.String("requested_language", requested),
		zap.String("association_status", string(status)))
}

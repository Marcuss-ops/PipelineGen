// Package scripts — ports/clip_text_track_ensurer.go: the canonical
// WRITE-side counterpart of TextTrackReader.
//
// Godlike/06 SSOT (one canonical owner per fact): TextTrackReader owns
// "what text is READY today"; ClipTextTrackEnsurer owns "make the text
// READY, creating and persisting it if it is not". The two are deliberately
// separate surfaces so a reader-only consumer (the prompt builder) cannot
// accidentally materialize translations, and so the one consumer that DOES
// need runtime materialization (the clip source resolver, when a script's
// language is not yet associated with the clip) has to opt in explicitly.
//
// Why this exists (item: "check script language / clip association"):
// a script can name a clip whose transcript exists in a DIFFERENT language,
// or does not exist at all. The historical behaviour was to fail closed with
// *ErrTextTrackNotReady (correct, but terminal). The ensurer is the seam that
// lets the resolver try to create + persist the missing text track at runtime
// through the ONE canonical materialization pipeline
// (texttracks.BackfillService: acquire → translate → save), then re-resolve.
//
// Godlike/07 NO-FAKE-AVAILABILITY: the ensurer returns an error when the
// track is still not READY when it returns. A nil ensurer means "runtime
// materialization is not available in this composition" — the resolver then
// keeps its pre-existing fail-closed behaviour instead of silently passing.
package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ClipTextTrackEnsurer guarantees that a READY text track exists for
// (assetID, languageCode, kind), creating AND persisting it when it is
// missing.
//
// Contract:
//   - nil error  ⇒ a READY track for the triple is guaranteed to exist
//     when the call returns (idempotent: an already-READY track is a no-op).
//   - non-nil    ⇒ the track is NOT READY. The call site MUST treat this as a
//     hard failure and surface the typed language-association error; there is
//     no "best effort" mode.
type ClipTextTrackEnsurer interface {
	EnsureReadyTextTrack(ctx context.Context, assetID, languageCode string, kind detail.TextTrackKind) error
}

// Package scripts — ports/text_track_reader.go: the canonical typed
// read surface for localized text tracks consumed by the video
// generation pipeline (ClipSourceBuilder).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026):
// Before Fase 4, ClipSourceBuilder read transcripts from
// `metadata_json["transcript"]` / `metadata_json["clean_transcript"]`
// — a pre-PR era convention that mixed the runtime DB (text_track
// source-of-truth) with the metadata-blob fallback. Fase 4 cuts the
// video pipeline over to read transcripts EXCLUSIVELY from
// `asset_text_tracks` via this port. The metadata_json read is
// RETIRED by default; a one-time migration flag
// (media.multilingual.migration_fallback_legacy_metadata) keeps the
// legacy path available until operators flip the cutover.
//
// godlike/06 SSOT (one canonical owner per fact): this interface is
// a SUB-INTERFACE of `detail.TextTrackRepository`. The concrete
// `*TextTrackRepositorySQLite` (in
// `internal/platform/sqlite/assets/`) satisfies both
// surfaces; the split is purely a wiring-boundary concern so the
// video pipeline imports a narrow read-only surface and not the
// full mutator surface (UpsertBatch / MarkPending / MarkFailed / etc).
//
// godlike/07 typed-error contract: the port does NOT define error
// sentinels. The `nil, nil` return convention is the canonical
// not-found signal (matching the Fase 2.a `FindReady` contract).
// Callers that need richer error surfaces wrap the result in their
// own typed errors (e.g. `ErrTextTrackNotReady` in
// `clip_source_builder_errors.go`).
package ports

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// TextTrackReader is the canonical read surface for the video
// generation pipeline. It is consumed by `ClipSourceBuilder` to
// resolve the transcript for each requested clip in the caller's
// target language. A nil receiver MUST fail-closed at the call site
// (BuildClipContext checks for nil before invoking — godlike/07
// NO-FAKE-AVAILABILITY).
//
// The 2 methods are 1:1 with the Fase 4 user spec:
//   - FindReady: lookup a single (asset, language, kind) track that
//     is in READY status. Returns (nil, nil) when no row exists OR
//     when the row exists but is in a non-READY status (PENDING /
//     FAILED). The READY-only filter is the canonical contract: a
//     non-READY row is not authoritative, and the pipeline surfaces
//     ErrTextTrackNotReady rather than using a stale row.
//   - ListReadyLanguages: enumerate the set of languages for which
//     a READY track exists for the given (asset, kind). Populates
//     the `AvailableLanguages` field of ErrTextTrackNotReady so
//     operator dashboards surface "what's actually READY" without
//     requiring a second round-trip.
//
// Implementations:
//   - Production: *asset.TextTrackRepositorySQLite (via the
//     `detail.TextTrackRepository` sub-interface).
//   - Tests: a hand-rolled stub mapping (asset, language, kind) →
//     *detail.TextTrack, optionally returning (nil, nil) for
//     not-found cases.
type TextTrackReader interface {
	// FindReady returns the READY text track for the given
	// (asset, language, kind) triple, plus the timed cues if
	// the track carries segment-level timing.
	//
	// Return contract:
	//   (track, cues, nil)  — track found and READY
	//   (nil, nil, nil)     — no track OR track not READY
	//   (nil, nil, err)     — repository-level error
	//
	// cues is nil when the source is payload-text, DB-stored
	// full-text, or Whisper (Whisper returns a single block,
	// no per-segment timing). cues is populated when the
	// source is a parsed VTT (YouTube subtitles) and was
	// persisted into asset_text_track_segments.
	FindReady(ctx context.Context, assetID string, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error)

	// ListReadyLanguages returns the sorted set of language
	// codes for which a READY track exists for the given
	// (asset, kind). Returns an empty slice (not nil) when no
	// READY tracks exist.
	ListReadyLanguages(ctx context.Context, assetID string, kind detail.TextTrackKind) ([]string, error)
}

// Compile-time assertion: the canonical production reader
// `*TextTrackRepositorySQLite` satisfies TextTrackReader via
// the detail.TextTrackRepository sub-interface. This is the
// AGENTS.md Pattern 0 build-time lock: a future signature drift
// in TextTrackReader surfaces as a build failure here, not as
// a runtime nil-method-call panic.
//
// Note: the assertion is in the ports/ package (not in the
// infrastructure/ package) because the ports/ package is the
// canonical consumer of TextTrackReader. Drift in the
// `detail.TextTrackRepository` sub-surface (which is the impl
// side) surfaces here as a build failure at the wire boundary
// — the canonical "drift detector" location per godlike/06.
var _ TextTrackReader = (detail.TextTrackRepository)(nil)

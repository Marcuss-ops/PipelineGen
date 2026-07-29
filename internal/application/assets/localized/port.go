// Package localized — port.go: LocalizedClipWriter application-layer
// port interface + the canonical typed command + typed errors
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - CommitLocalizedClipCommand shape lives here (cross-cutting:
//     combines youtubedto.ClipAsset + asset.TextTrack + asset.TimedCue).
//   - LocalizedClipWriter port lives here (the application-layer
//     surface every concrete adapter MUST satisfy).
//   - ErrClipLocaleNotReady typed error lives here (canonical
//     probe target for callers — Fase 5 backfill CLI + Fase 4
//     video pipeline errors.As-probe it).
//
// godlike/07 no-fake-availability: the port object's nil-check
// pattern is NOT used here — implementations MUST be safe to
// call through this interface. The composition root wires the
// concrete (ClipAtomicWriterAdapter); production code does NOT
// reach around the port.
package localized

import (
	"context"
	"errors"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// LocalizedClipWriter is the application-layer port for the
// ATOMIC clip + text tracks + segments + outbox write
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b). The concrete
// implementation owns a single SQLite tx that performs:
//
//  1. UPSERT media_assets
//  2. UPSERT asset_text_tracks (RETURNING id for FK resolution)
//  3. BATCH INSERT asset_text_track_segments (in sequence_no order)
//  4. INSERT outbox_events
//  5. COMMIT (or ROLLBACK on any step failure)
//
// godlike/06 SSOT: This port is the SOLE canonical surface for
// the localized-write super-tx. The legacy
// youtubeports.ClipAtomicWriter (CommitClipAndIndexEvent) is
// retained as a STRIPE for callers that don't carry localized
// text (e.g. announcement text-only writes) — see
// internal/application/assets/persistence/writer.go for the
// cross-package alignment story.
type LocalizedClipWriter interface {
	CommitClipTextAndIndexEvent(ctx context.Context, cmd CommitLocalizedClipCommand) error
}

// CommitLocalizedClipCommand bundles the inputs for the
// atomic-transactional clip+tracks+segments+outbox write. All
// fields are mandatory except TimedTracks and the optional
// Require*Policy booleans.
//
// godlike/06 SSOT: This command is the canonical application
// input to LocalizedClipWriter. No handler may construct an
// alternative shape inline.
type CommitLocalizedClipCommand struct {
	// Clip is the canonical media_assets row shape. Same DTO used
	// by YouTube's Step 9 writer (godlike/06 SSOT: ClipAsset is
	// the canonical cross-call-site entity).
	Clip youtubetypes.ClipAsset

	// TextTracks are the per-language text resources to persist
	// in asset_text_tracks — one row per (asset_id,
	// language_code, text_kind). Empty slice is allowed
	// (callers MAY skip the asset_text_tracks upsert).
	TextTracks []asset.TextTrack

	// TimedTracks bundle the per-cue timings to persist in
	// asset_text_track_segments — one row per (track_id,
	// sequence_no). Cues MUST be ordered ascending by start_ms;
	// the writer assigns sequence_no based on this order.
	// Empty slice is allowed.
	TimedTracks []TimedTextTrack

	// IndexEvent carries only commit metadata (AggregateID + CreatedAt).
	// The canonical AssetCommitter owns the event type and builds the
	// payload internally; callers cannot supply a custom envelope.
	IndexEvent youtubeports.IndexEventPayload

	// RequireTranscriptReady, when true, makes the writer fail
	// with ErrClipLocaleNotReady BEFORE the tx (no rows written)
	// if no transcript-origin track (text_kind=transcript,
	// status=READY, source_type in {provided,
	// youtube_subtitle, whisper}) is present in TextTracks.
	// Policy: media.multilingual.require_all_before_video=true.
	RequireTranscriptReady bool

	// RequireAllLanguagesBeforeVideo, when set together with
	// PreferredLanguages, asserts that every language in the
	// PreferredLanguages list has a READY track (text_kind=
	// transcript). Failure yields ErrClipLocaleNotReady. The
	// check is additive — RequireTranscriptReady is the
	// transcript-existence invariant; this is the per-language
	// coverage invariant. Both are evaluated pre-tx.
	RequireAllLanguagesBeforeVideo bool
	PreferredLanguages             []string
}

// TimedTextTrack groups timed cues for a single asset_text_tracks
// row. The writer matches on (LanguageCode, TextKind,
// SourceType) to associate with the corresponding track_id
// (resolved at write time via asset_text_tracks UPSERT's
// RETURNING clause).
//
// godlike/06 SSOT: Cues are ordered by the writer — callers MAY
// pass them in any order; the writer sorts ascending by StartMs
// before assigning SequenceNo. This makes the bundle's array
// index irrelevant to the persisted order (FSM-friendly: the
// resolver can re-order cues without breaking uniqueness).
type TimedTextTrack struct {
	LanguageCode string
	TextKind     asset.TextTrackKind
	SourceType   asset.TextTrackSource
	Cues         []asset.TimedCue
}

// ErrClipLocaleNotReady is returned pre-tx (no rows written) when
// the command's RequireTranscriptReady or
// RequireAllLanguagesBeforeVideo flag is set and the payload's
// TextTracks does not satisfy the policy.
//
// godlike/06 SSOT: this is the SOLE typed-error surface for the
// multilingual localisation policy. Other layers' errors
// (text-track save failed, FK violation, UNIQUE collision, etc.)
// propagate as the underlying SQLite error wrapped in
// fmt.Errorf with the writer's prefix — they MUST NOT use this
// type.
//
// Callers (Fase 5 backfill, Fase 4 video pipeline) errors.As-probe
// this to distinguish "backfill pending" from generic writer
// failures (which require retry/escalation).
type ErrClipLocaleNotReady struct {
	AssetID      string
	Reason       string
	MissingKind  asset.TextTrackKind
	MissingCodes []string // populated only for RequireAllLanguagesBeforeVideo
}

func (e *ErrClipLocaleNotReady) Error() string {
	if len(e.MissingCodes) > 0 {
		return "clip locale not ready: asset=" + e.AssetID +
			" reason=" + e.Reason +
			" missing_kind=" + string(e.MissingKind) +
			" missing_languages=" + stringsJoin(e.MissingCodes, ",")
	}
	return "clip locale not ready: asset=" + e.AssetID +
		" reason=" + e.Reason +
		" missing_kind=" + string(e.MissingKind)
}

// IsClipLocaleNotReady is the canonical probe used by callers.
// errors.As-probes the target type.
func IsClipLocaleNotReady(err error) bool {
	var target *ErrClipLocaleNotReady
	return errors.As(err, &target)
}

// stringsJoin is a tiny helper kept local to avoid pulling strings
// into every consumer. The implementation is equivalent to
// strings.Join(s, sep) but bounds the import surface.
func stringsJoin(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	out := s[0]
	for i := 1; i < len(s); i++ {
		out += sep + s[i]
	}
	return out
}

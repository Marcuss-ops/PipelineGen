package assets

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CommitClipTextAndIndexEvent performs the canonical atomic localized
// clip write (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b, July 2026):
//
//	BEGIN
//	VALIDATE cmd.Require* flags (NO rows written on ErrClipLocaleNotReady)
//	UPSERT media_assets               (id=clipID, lifecycle_state='ACTIVE')
//	UPSERT asset_text_tracks          (per row, RETURNING id)
//	BATCH INSERT asset_text_track_segments (per TimedTextTrack, sequence_no
//	                                       sorted ascending by StartMs)
//	BUILD  eventKey, payload = BuildReindexEnvelopeV1(clipID, targetSchema=…,
//	                                                 sourceVersion=…)
//	INSERT outbox_events              (ON CONFLICT(event_key) DO NOTHING)
//	COMMIT
//
// godlike/06 SSOT: this is the SOLE canonical atomic surface for the
// localized-clip write. The legacy CommitClipAndIndexEvent (above) is
// retained for callers that DON'T carry localized text (announcement
// / stock-without-i18n paths) — but every caller that has a
// localized payload MUST use this method.
//
// godlike/07 no-fake-availability: a typed error is returned for
// every abort path; nil is only returned when the full super-tx has
// committed atomically.
//
// The validation phase (commitClipTextAndIndexEvent_validatePolicy)
// runs BEFORE the tx starts. Any ErrClipLocaleNotReady is
// returned to the caller without opening a tx — the writer never
// partially applies the policy.
func (w *ClipAtomicWriterAdapter) CommitClipTextAndIndexEvent(
	ctx context.Context,
	cmd localized.CommitLocalizedClipCommand,
) error {
	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: adapter not wired")
	}
	if cmd.Clip.ID == "" {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: cmd.Clip.ID is required")
	}

	// ── 0) Validate policy BEFORE the tx (no rows written).
	if verr := commitClipTextAndIndexEvent_validatePolicy(cmd); verr != nil {
		if w.log != nil {
			w.log.Warn("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: locale policy violated; rolling back (no rows written)",
				zap.String("clip_id", cmd.Clip.ID), zap.Error(verr))
		}
		return verr
	}

	// ── 1) Begin tx (orchestrator-owned).
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── 2) UPSERT media_assets + outbox via AssetCommitter.
	nowStr := w.now().UTC().Format(time.RFC3339)
	req, err := w.buildCommitRequest(cmd.Clip.ID, cmd.Clip)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: build commit request: %w", err)
	}
	res, err := w.committer.CommitTx(ctx, tx, req)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: commit asset: %w", err)
	}

	// ── 3) UPSERT asset_text_tracks (RETURNING id for FK resolution).
	// The track-id map is the canonical source of truth for the
	// BATCH INSERT in step (4). Match key (language_code, text_kind,
	// source_type) — the writer expects every TimedTextTrack to
	// have a corresponding TextTrack row (validated pre-tx by
	// reviewers at the cmd-construction site; the writer surfaces
	// a typed error if a TimedTextTrack has no matching parent row).
	trackIDByKey, terr := upsertTextTracksReturningIDsInTx(ctx, tx, cmd.TextTracks, nowStr)
	if terr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: upsert text tracks: %w", terr)
	}

	// ── 4) BATCH INSERT asset_text_track_segments.
	if serr := insertTextTrackSegmentsInTx(ctx, tx, cmd.TimedTracks, trackIDByKey); serr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: insert segments: %w", serr)
	}

	// ── 7) Commit (orchestrator-owned).
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: commit: %w", cerr)
	}
	committed = true

	// ── 8) BLOCKER #4 closure: terminal conflict → typed error.
	if terr := checkOutboxTerminalAfterCommit(w.log, res.OutboxInserted, cmd.Clip.ID, res.OutboxEventKey, res.OutboxExistingStatus); terr != nil {
		return terr
	}

	if w.log != nil {
		w.log.Debug("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: clip + tracks + segments + index event committed atomically",
			zap.String("clip_id", cmd.Clip.ID),
			zap.String("event_key", res.OutboxEventKey),
			zap.Int("text_tracks", len(cmd.TextTracks)),
			zap.Int("timed_tracks", len(cmd.TimedTracks)),
			zap.Bool("outbox_inserted", res.OutboxInserted))
	}
	return nil
}

// commitClipTextAndIndexEvent_validatePolicy checks the
// Require* flags WITHOUT opening the tx. Returns a typed
// *localized.ErrClipLocaleNotReady when the policy is violated
// (no rows written, no outbox event created).
//
// godlike/06 SSOT: this is the SOLE place where text-track policy
// lives. The caller MUST NOT pre-check; the writer rejects
// invalid commands atomically.
//
// Godlike/07 honest lock: the policy is evaluated against
// TextTracks in the command (which the resolver has already
// inflated from the DB / payload / chain). The check is
// structural — every policy failure yields an actionable
// `MissingLanguages` or `Reason` payload.
//
// godlike/10 single-orchestrator-invariants: business branching
// stays in this orchestrator file. The split into per-table
// siblings keeps the helpers as pure SQL-composition functions;
// the policy SSOT lives here so future port migrations (e.g.
// adding a `LocalizedClipWriter` interface check) coordinate
// through one place.
//
// Audit 2026-07-11 §2.e: the prior filter accepted row-2's
// IsOriginal=false rows when SourceType=provided, which
// let a caller smuggle an "original" through a translation
// row. The strict IsOriginal=true invariant below closes the
// bypass (godlike/07 typed-error contract: the writer NEVER
// silently allows partial-state operations).
func commitClipTextAndIndexEvent_validatePolicy(cmd localized.CommitLocalizedClipCommand) error {
	if !cmd.RequireTranscriptReady && !cmd.RequireAllLanguagesBeforeVideo {
		return nil
	}

	// Track which languages have a READY transcript-origin track
	// available. transcript-origin = source_type in {provided,
	// youtube_subtitle, whisper}; status = READY; text_kind =
	// transcript (the canonical transcript row); AND IsOriginal
	// must be true (a TRANSLATION row that misreports its
	// SourceType as `provided` does NOT satisfy the
	// transcript-origin requirement — only true originals do).
	readyLangs := make(map[string]bool)
	hasTranscriptReady := false
	for _, t := range cmd.TextTracks {
		if t.TextKind != asset.TextTrackTranscript {
			continue
		}
		if t.Status != asset.TextTrackReady {
			continue
		}
		if t.SourceType != asset.TextSourceProvided &&
			t.SourceType != asset.TextSourceYouTubeSubtitle &&
			t.SourceType != asset.TextSourceWhisper {
			continue
		}
		if !t.IsOriginal {
			continue
		}
		readyLangs[t.LanguageCode] = true
		hasTranscriptReady = true
	}

	if cmd.RequireTranscriptReady && !hasTranscriptReady {
		return &localized.ErrClipLocaleNotReady{
			AssetID:     cmd.Clip.ID,
			Reason:      "no transcript-origin READY track (provided/youtube_subtitle/whisper text_kind=transcript status=READY) in command.TextTracks",
			MissingKind: asset.TextTrackTranscript,
		}
	}

	if cmd.RequireAllLanguagesBeforeVideo && len(cmd.PreferredLanguages) > 0 {
		var missing []string
		for _, lang := range cmd.PreferredLanguages {
			if !readyLangs[lang] {
				missing = append(missing, lang)
			}
		}
		if len(missing) > 0 {
			return &localized.ErrClipLocaleNotReady{
				AssetID:      cmd.Clip.ID,
				Reason:       "missing READY translations for one or more PreferredLanguages",
				MissingKind:  asset.TextTrackTranscript,
				MissingCodes: missing,
			}
		}
	}

	return nil
}

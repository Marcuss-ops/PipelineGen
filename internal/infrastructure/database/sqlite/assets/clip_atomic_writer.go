// Package assets — clip_atomic_writer.go: ClipAtomicWriterAdapter
// concrete implementation of the youtubeports.ClipAtomicWriter typed
// interface.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the canonical
// ProcessYouTubeSegmentUseCase (internal/application/youtube/usecase/
// process_segment.go) calls the atomic writer at Step 9 as its
// terminal step. Before Commit 1 the adapter was unbound — the use
// case's nil-port path silently produced `out.Status = "processed"`
// even when DB + outbox writes were skipped (P0 #3 in the compliance
// verdict).
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #6): the
// CommitClipAndIndexEvent parameter is now `youtubetypes.ClipAsset`
// (the canonical, strongly-typed internal domain entity) instead of
// `youtubetypes.ExtractItem` (the HTTP response shape). The
// verdict's P1 #6 mandates "il writer deve ricevere il record
// canonico, non un DTO di risposta HTTP" — ClipAsset bundles the
// ID/VideoID/LocalPath/FileHash/Drive/Coordinates/Metadata fields
// the writer needs in one typed struct so the DB column mapping is
// explicit and refactor-resistant.
//
// Transaction shape (canonical PR-C PR-VO-A3 pattern):
//
//	BEGIN
//	UPSERT media_assets SET  ... (id=clipID, lifecycle_state='ACTIVE',
//	                                  source_version=deriveSourceVersion(...))
//	BUILD  eventKey, payload = BuildReindexEnvelopeV1(
//	          clipID, targetSchema="asset.index.requested.v1",
//	          sourceVersion=deriveSourceVersion(clipID, asset.FileHash, asset.PolicyVersion),
//	          requestedAt=now)
//	INSERT outbox_events (...) ON CONFLICT(event_key) WHERE !='' DO NOTHING
//	COMMIT
//
// Audit 2026-07-03 BLOCKER #2 closure: source_version is now written
// to BOTH the media_assets.source_version column (via upsertClipInTx)
// AND the outbox event envelope (BuildReindexEnvelopeV1). The CAS
// fence in clipindexer.setIndexedAt reads source_version from the
// column; before this fix the column was always ” (default) while
// the event carried the real value — the CAS fence starved.
//
// Idempotency contracts (mirrored from outboxevents.BuildReindexEnvelopeV1):
//   - eventKey shaped "reconcile:reindex:<assetID>:<schema>:<source>".
//     Repeated calls with the same (clipID, file_hash, policy) tuple
//     collapse via outbox ON CONFLICT(event_key) DO NOTHING — only one
//     outbox row exists for any (clip, content) pair.
//   - Different file_hash on retry → new eventKey → new outbox row.
//     The supersede gate downstream (outbox.Pool / IndexingHandler)
//     compares payload.source_version against the current
//     media_assets.content_hash and rejects stale events as supersede.
//   - Empty sourceVersion is fail-closed at BuildReindexEnvelopeV1.
//     We compute sourceVersion = asset.FileHash (the canonical
//     ingest-time hash). On empty FileHash (the upstream
//     hash.MD5File skipped path) we fall back to a deterministic
//     MD5(clipID + policyVersion) so the event_key remains stable
//     across retries.
//
// Column projection (11 fields): the canonical ClipAsset → media_assets
// surface. LIVE state is updated_at + updated-once fields; lifecycle_state
// stays 'ACTIVE' (the canonical PR-C lifecycle) — soft-delete is delegated
// to LifecycleService. We intentionally do NOT include the metadata_json
// write side: ClipAtomicWriter is the bare writer bridge for the clip
// write; the metadata enrichment path is a distinct Phase (Commit 4).
package assets

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ClipAtomicWriterAdapter implements youtubeports.ClipAtomicWriter over
// the canonical SQLite + outbox events schema. Holds *sql.DB (the
// ledger connection) and *outboxevents.Repository (the outbox writer
// — talks into the SAME connection within the same tx per the
// outboxevents.Repository.Enqueue contract).
type ClipAtomicWriterAdapter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	now func() time.Time // injectable clock for tests; production = time.Now
}

// NewClipAtomicWriterAdapter constructs the adapter. Both db AND box
// MUST be non-nil — a nil either side is a fail-closed panic so a
// wiring gap lands in a build-side output rather than a runtime panic
// at first CommitClipAndIndexEvent call.
func NewClipAtomicWriterAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *ClipAtomicWriterAdapter {
	if db == nil {
		panic("assets.NewClipAtomicWriterAdapter: db is required (composition must pass root.DB.DB)")
	}
	if box == nil {
		panic("assets.NewClipAtomicWriterAdapter: outboxevents.Repository is required (composition must pass root.Outbox.EventsRepo)")
	}
	return &ClipAtomicWriterAdapter{
		db:  db,
		box: box,
		log: log,
		now: time.Now,
	}
}

// CommitClipAndIndexEvent performs the canonical atomic write.
//
// Commit 2/6: the parameter is `youtubetypes.ClipAsset` (canonical
// domain entity). The ClipAsset's Drive / Coordinates / Metadata
// fields are the canonical writer surface; the column mapping in
// `upsertClipInTx` reads from ClipAsset's nested structs.
func (w *ClipAtomicWriterAdapter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	asset youtubetypes.ClipAsset,
	event youtubeports.IndexEventPayload,
) error {
	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: adapter not wired")
	}
	if clipID == "" {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: clipID is required")
	}
	if event.Type != "" && event.Type != outboxevents.EventAssetIndexRequested {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: unsupported event.Type %q (only %q supported)",
			event.Type, outboxevents.EventAssetIndexRequested)
	}
	if event.Type == "" {
		event.Type = outboxevents.EventAssetIndexRequested
	}
	if event.AggregateID == "" {
		event.AggregateID = clipID
	}

	// ── 1) Begin tx
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── 2) UPSERT media_assets (BLOCKER #2 closure: source_version
	//     is now written to the DB column, not just the outbox envelope).
	nowStr := w.now().UTC().Format(time.RFC3339)
	// Compute before both UPSERT + outbox so both surfaces agree on
	// the same sourceVersion — invariant enforced by the test.
	policyVersion := asset.PolicyVersion
	if policyVersion == "" {
		policyVersion = derivePolicyVersion(clipID)
	}
	sourceVersion := deriveSourceVersion(clipID, asset.FileHash, policyVersion)
	if err := upsertClipInTx(ctx, tx, clipID, asset, sourceVersion, nowStr); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert: %w", err)
	}

	// ── 2.5) UPSERT asset_text_tracks from payload Texts[].
	// When the caller provided localized texts (transcripts, descriptions,
	// etc.) in the Segment.Texts[] field, persist them atomically in the
	// same transaction as media_assets + outbox_events. This eliminates
	// the race where a separate TextTrackResolver.Save() call could fail
	// silently after Step 9 committed.
	if len(asset.Texts) > 0 {
		tracks := localizedClipTextsToTextTracks(clipID, asset.Texts)
		if len(tracks) > 0 {
			if err := upsertTextTracksInTx(ctx, tx, tracks, nowStr); err != nil {
				return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert text tracks: %w", err)
			}
		}
	}

	// ── 3) Build canonical v1 envelope
	eventKey, payloadJSON, err := outboxevents.BuildReindexEnvelopeV1(
		clipID,
		outboxevents.ReindexEnvelopeV1Schema,
		sourceVersion,
		w.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: build envelope: %w", err)
	}
	// Blocco 1.1: the canonical envelope built by BuildReindexEnvelopeV1
	// always wins. The caller MUST NOT supply a custom payload — the
	// IndexEventPayload carries only routing fields (Type, AggregateID,
	// CreatedAt). The previous `if len(event.Payload) > 0` override path
	// replaced the canonical envelope with an ad-hoc payload that the
	// IndexingHandler consumer rejected as terminal (dead_letter).

	// ── 4) INSERT outbox_events (tx-bound)
	enqResult, err := w.box.Enqueue(
		ctx,
		tx,
		outboxevents.EventAssetIndexRequested,
		clipID,
		"media_asset",
		payloadJSON,
		eventKey,
	)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: outbox enqueue: %w", err)
	}

	// ── 5) Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: commit: %w", err)
	}
	committed = true

	// ── 6) BLOCKER #4 closure: terminal conflict → error, not silent success.
	// Pre-closure the writer logged a warning and returned nil, producing
	// "processed" with no index event. Post-closure we return a typed
	// sentinel so the use case can surface "processed_but_index_blocked".
	if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
		err := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing %q row (event_id=%d)",
			youtubeports.ErrOutboxTerminalConflict, clipID, eventKey,
			enqResult.ExistingStatus, enqResult.EventID)
		if w.log != nil {
			w.log.Warn("ClipAtomicWriterAdapter: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus),
				zap.Error(err))
		}
		return err
	}

	if w.log != nil {
		w.log.Debug("ClipAtomicWriterAdapter: clip + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("source_version", sourceVersion),
			zap.Bool("outbox_inserted", enqResult.Inserted))
	}
	return nil
}

// localizedClipTextsToTextTracks converts payload-provided
// LocalizedClipText entries into domain TextTrack rows suitable
// for upsertTextTracksInTx. Each non-empty text field
// (Transcript, Description, Summary, Title) produces a separate
// TextTrack entry with the corresponding TextKind.
func localizedClipTextsToTextTracks(clipID string, texts []youtubetypes.LocalizedClipText) []asset.TextTrack {
	if len(texts) == 0 {
		return nil
	}
	var tracks []asset.TextTrack
	for _, t := range texts {
		lang := t.LanguageCode
		if lang == "" {
			lang = "en"
		}
		srcType := asset.TextTrackSource(t.SourceType)
		if srcType == "" {
			srcType = asset.TextSourceProvided
		}
		isOriginal := t.IsOriginal
		if srcType == asset.TextSourceProvided {
			isOriginal = true
		}

		type entry struct {
			kind    asset.TextTrackKind
			content string
		}
		entries := []entry{
			{asset.TextTrackTranscript, t.Transcript},
			{"description", t.Description},
			{"summary", t.Summary},
			{"title", t.Title},
		}
		for _, e := range entries {
			if e.content == "" {
				continue
			}
			var confidence *float64
			if t.Confidence > 0 {
				confidence = &t.Confidence
			}
			tracks = append(tracks, asset.TextTrack{
				AssetID:            clipID,
				LanguageCode:       lang,
				TextKind:           e.kind,
				TextContent:        e.content,
				SourceType:         srcType,
				SourceLanguageCode: t.SourceLanguageCode,
				IsOriginal:         isOriginal,
				ModelName:          t.ModelName,
				ModelVersion:       t.ModelVersion,
				Confidence:         confidence,
				Status:             asset.TextTrackReady,
			})
		}
	}
	return tracks
}

// isTerminalOutboxStatus reports whether an outbox row's status is
// terminal — useful for deciding whether a fresh INSERT was squelched
// by an already-completed/failed event, vs the more benign case
// where the same key was already in pending/processing.
func isTerminalOutboxStatus(status string) bool {
	return status == "dead_letter" || status == outboxevents.SupersedeStatus
}

// upsertClipInTx writes the canonical 11-column clip row shape into
// media_assets inside the caller's tx. Audit 2026-07-03 BLOCKER #2
// closure: source_version is now included in both INSERT and
// ON CONFLICT DO UPDATE, matching the outbox event's source_version
// so the CAS fence in clipindexer.setIndexedAt can read a non-empty
// value.
func upsertClipInTx(ctx context.Context, tx *sql.Tx, clipID string, asset youtubetypes.ClipAsset, sourceVersion, nowStr string) error {
	if tx == nil {
		return errors.New("upsertClipInTx: tx is nil")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			drive_file_id, drive_link, download_link,
			local_path, file_hash,
			folder_id, folder_path,
			source_version, search_text,
			lifecycle_state, updated_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			local_path = excluded.local_path,
			file_hash = excluded.file_hash,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			source_version = excluded.source_version,
			search_text = excluded.search_text,
			updated_at = excluded.updated_at
	`,
		clipID,
		"youtube",
		routeEmpty(deriveNameFromAsset(asset), clipID),
		routeEmpty(deriveFilenameFromAsset(asset), clipID+".mp4"),
		"video",
		asset.Drive.FileID,
		asset.Drive.WebViewLink,
		"", // download_link — derived from FileID in production; left empty in Commit 2
		asset.LocalPath,
		asset.FileHash,
		asset.Drive.FolderID,
		routeEmpty(asset.Drive.FolderPath, asset.Drive.FolderID),
		sourceVersion,
		routeEmpty(asset.SearchText, ""),
		"ACTIVE",
		nowStr,
		nowStr,
	)
	if err != nil {
		return err
	}
	return nil
}

// deriveNameFromAsset returns a canonical name for the clip row.
// Pulls from asset.Metadata.Summary if non-empty, otherwise falls
// back to the asset ID. Kept private because the column mapping
// is an internal detail of the writer adapter.
func deriveNameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.Metadata.Summary != "" {
		return asset.Metadata.Summary
	}
	return ""
}

// deriveFilenameFromAsset returns the canonical filename for the
// clip row. Builds from the slug (asset.Metadata.Summary) if present,
// otherwise falls back to the canonical yt_<videoID>_<start>_<end>
// shape derived from the asset Coordinates. The full policy-versioned
// filename is set on the use case side via BuildClipFilename; the
// writer's filename is the basename of the local file when available.
func deriveFilenameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.LocalPath != "" {
		return filepathBase(asset.LocalPath)
	}
	return ""
}

// routeEmpty is the canonical "fallback-to-this-string" helper for
// INSERT columns where an empty value would later fail a NOT NULL
// check. Kept as a private helper because tests can construct
// ClipAssets with empty Name and the adapter must keep the row
// insertable.
func routeEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// derivePolicyVersion extracts the policy_version suffix from a
// canonical clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>").
// Returns "v1" when the suffix is missing (legacy / build error /
// hand-crafted clipID) so the source-version fallback remains stable
// across retries.
func derivePolicyVersion(clipID string) string {
	const wantUnderscores = 4
	seen := 0
	for i := len(clipID) - 1; i >= 0; i-- {
		if clipID[i] == '_' {
			seen++
			if seen == wantUnderscores {
				pv := clipID[i+1:]
				if pv != "" {
					return pv
				}
				return "v1"
			}
		}
	}
	return "v1"
}

// deriveSourceVersion returns the canonical ingest-time content hash
// fingerprint used as event.source_version. In priority order:
//  1. asset.FileHash (the canonical MD5 of the local clip file).
//  2. fallback = MD5(clipID + ":" + policyVersion) — invariant under
//     retries so ON CONFLICT(event_key) collapses into a single row.
func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
	if fileHash != "" {
		return fileHash
	}
	h := md5.Sum([]byte(clipID + ":" + policyVersion))
	return hex.EncodeToString(h[:])
}

// filepathBase is a thin wrapper around path/filepath.Base that
// avoids importing path/filepath at the top of the file. The local
// path is always absolute in production, so the Base call is safe.
//
// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza): the local wrapper
// was removed in favour of stdlib path/filepath.Base per the
// code-reviewer critical finding. The previous hand-rolled loop
// had the same string contract on absolute Unix paths but missed
// Windows backslash handling and edge cases for trailing
// separators; stdlib's filepath.Base is the canonical implementation.
func filepathBase(p string) string {
	return filepath.Base(p)
}

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift surfaces as a build failure.
var _ youtubeports.ClipAtomicWriter = (*ClipAtomicWriterAdapter)(nil)

// ── Per AGENTS.md Pattern 0 (second port): the concrete receiver
// INTENT-DUAL-PORT satisfies the localized-clip atomic writer port
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b, July 2026). The exact
// same concrete instance is wired by the composition root through
// BOTH ports (processSegmentDeps.Writer AND
// processSegmentDeps.LocalizedWriter) — the dual-port pattern is
// intentional: the legacy stripe (CommitClipAndIndexEvent, no text
// tracks) and the new atomic-super-tx stripe
// (CommitClipTextAndIndexEvent, clip+tracks+cues+outbox) both
// resolve to the same *ClipAtomicWriterAdapter which keeps a single
// SQL connection pool + a single outbox.Repository handle. Adding
// a parallel adapter would double the connection pool and risk
// dead-letter routing divergence between the two surfaces — this
// dual-port assertion is the SSOT that defeats that divergence.
var _ localized.LocalizedClipWriter = (*ClipAtomicWriterAdapter)(nil)

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

	// ── 1) Begin tx
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

	// ── 2) UPSERT media_assets (BLOCKER #2 closure: source_version
	//     written to the DB column, mirroring the outbox envelope).
	nowStr := w.now().UTC().Format(time.RFC3339)
	policyVersion := cmd.Clip.PolicyVersion
	if policyVersion == "" {
		policyVersion = derivePolicyVersion(cmd.Clip.ID)
	}
	sourceVersion := deriveSourceVersion(cmd.Clip.ID, cmd.Clip.FileHash, policyVersion)
	if uerr := upsertClipInTx(ctx, tx, cmd.Clip.ID, cmd.Clip, sourceVersion, nowStr); uerr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: upsert clip: %w", uerr)
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

	// ── 5) Build canonical v1 outbox envelope.
	eventKey, payloadJSON, berr := outboxevents.BuildReindexEnvelopeV1(
		cmd.Clip.ID,
		outboxevents.ReindexEnvelopeV1Schema,
		sourceVersion,
		w.now().UTC(),
	)
	if berr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: build envelope: %w", berr)
	}

	// ── 6) INSERT outbox_events (tx-bound).
	eventType := cmd.IndexEvent.Type
	if eventType == "" {
		eventType = outboxevents.EventAssetIndexRequested
	}
	aggregateID := cmd.IndexEvent.AggregateID
	if aggregateID == "" {
		aggregateID = cmd.Clip.ID
	}
	enqResult, eerr := w.box.Enqueue(
		ctx,
		tx,
		eventType,
		aggregateID,
		"media_asset",
		payloadJSON,
		eventKey,
	)
	if eerr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: outbox enqueue: %w", eerr)
	}

	// ── 7) Commit
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: commit: %w", cerr)
	}
	committed = true

	// ── 8) BLOCKER #4 closure: terminal conflict → typed error.
	if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
		errOutbox := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing %q row (event_id=%d)",
			youtubeports.ErrOutboxTerminalConflict, cmd.Clip.ID, eventKey,
			enqResult.ExistingStatus, enqResult.EventID)
		if w.log != nil {
			w.log.Warn("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
				zap.String("clip_id", cmd.Clip.ID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus),
				zap.Error(errOutbox))
		}
		return errOutbox
	}

	if w.log != nil {
		w.log.Debug("ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent: clip + tracks + segments + index event committed atomically",
			zap.String("clip_id", cmd.Clip.ID),
			zap.String("event_key", eventKey),
			zap.String("source_version", sourceVersion),
			zap.Int("text_tracks", len(cmd.TextTracks)),
			zap.Int("timed_tracks", len(cmd.TimedTracks)),
			zap.Bool("outbox_inserted", enqResult.Inserted))
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
	//
	// Audit 2026-07-11 §2.e: the prior filter accepted row-2's
	// IsOriginal=false rows when SourceType=provided, which
	// let a caller smuggle an "original" through a translation
	// row. The strict IsOriginal=true invariant below closes the
	// bypass (godlike/07 typed-error contract: the writer NEVER
	// silently allows partial-state operations).
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

// upsertTextTracksReturningIDsInTx performs the asset_text_tracks
// UPSERT inside the caller's tx, capturing the assigned track_id
// (via RETURNING id) for each row. The returned map is keyed by
// (language_code + "|" + text_kind + "|" + source_type) so the
// step-(4) segments batch INSERT can resolve parent FKs.
//
// godlike/06 SSOT: the upsert SQL mirrors upsertTextTracksInTx
// (clip_metadata_writer.go) but adds the RETURNING clause. The
// hash + source_version columns are populated from the row's
// TextHash / SourceVersion fields; callers MUST have invoked the
// canonical hash factory (internal/domain/asset/text_track_hashes.go).
// Re-deriving the SHA-256 inline is forbidden (see the SSOT
// contract on text_track_hashes.go).
func upsertTextTracksReturningIDsInTx(
	ctx context.Context,
	tx *sql.Tx,
	tracks []asset.TextTrack,
	nowStr string,
) (map[string]int64, error) {
	trackIDByKey := make(map[string]int64, len(tracks))
	if len(tracks) == 0 {
		return trackIDByKey, nil
	}

	upsertSQL := `
INSERT INTO asset_text_tracks (
    asset_id, language_code, text_kind,
    text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
    confidence           = excluded.confidence,
    status               = excluded.status,
    updated_at           = datetime('now')
RETURNING id`

	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		if t.AssetID == "" || t.LanguageCode == "" || t.TextKind == "" {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: row missing required keys (AssetID/LanguageCode/TextKind)")
		}

		var confidence interface{}
		if t.Confidence != nil {
			confidence = *t.Confidence
		}

		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}
		status := string(t.Status)
		if status == "" {
			status = string(asset.TextTrackReady)
		}

		var id int64
		scanErr := stmt.QueryRowContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.TextHash,
			t.SourceVersion,
			confidence,
			status,
		).Scan(&id)
		if scanErr != nil {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, scanErr)
		}

		key := textTrackKey(t.LanguageCode, t.TextKind, t.SourceType)
		trackIDByKey[key] = id
	}
	return trackIDByKey, nil
}

// textTrackKey is the canonical key used by the writer to match
// TimedTextTrack entries with their parent TextTrack rows. The
// canonical key shape is (language_code + "|" + text_kind + "|" +
// source_type) — three fields are required because source_type is
// part of the unique-write contract for the asset_text_tracks
// table (a clip may have multiple tracks per (lang, kind) if
// they come from different sources; e.g. a user-provided
// transcript AND a YouTube-subtitle generated track).
func textTrackKey(language string, kind asset.TextTrackKind, source asset.TextTrackSource) string {
	return strings.Join([]string{language, string(kind), string(source)}, "|")
}

// insertTextTrackSegmentsInTx performs the BATCH INSERT of
// asset_text_track_segments, one row per cue. Cues are sorted
// ascending by StartMs BEFORE assigning sequence_no (UNIQUE
// constraint enforcement). Each TimedTextTrack MUST resolve to
// a parent text track via trackIDByKey; the writer surfaces a
// typed error when no match is found.
//
// godlike/06 SSOT: sequence_no is assigned in-memory by this
// function. The DB has a UNIQUE(track_id, sequence_no) constraint;
// the writer also avoids negative or non-monotonic sequence_no so
// the persistence order is stable across retries.
func insertTextTrackSegmentsInTx(
	ctx context.Context,
	tx *sql.Tx,
	timedTracks []localized.TimedTextTrack,
	trackIDByKey map[string]int64,
) error {
	if len(timedTracks) == 0 {
		return nil
	}

	insertSQL := `
INSERT INTO asset_text_track_segments (
    track_id, sequence_no, start_ms, end_ms, text
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(track_id, sequence_no) DO UPDATE SET
    start_ms = excluded.start_ms,
    end_ms   = excluded.end_ms,
    text     = excluded.text`

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("insertTextTrackSegmentsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, tt := range timedTracks {
		key := textTrackKey(tt.LanguageCode, tt.TextKind, tt.SourceType)
		trackID, ok := trackIDByKey[key]
		if !ok {
			return fmt.Errorf("insertTextTrackSegmentsInTx: timed track has no matching TextTrack (lang=%s kind=%s source=%s) — ensure TextTracks has the parent row",
				tt.LanguageCode, tt.TextKind, tt.SourceType)
		}

		// Sort cues ascending by StartMs so sequence_no is
		// monotonic. Use SliceStable so equal-start cues preserve
		// caller order (deterministic behaviour across retries).
		sortedCues := append([]asset.TimedCue(nil), tt.Cues...)
		sort.SliceStable(sortedCues, func(i, j int) bool {
			if sortedCues[i].StartMs != sortedCues[j].StartMs {
				return sortedCues[i].StartMs < sortedCues[j].StartMs
			}
			return sortedCues[i].EndMs < sortedCues[j].EndMs
		})

		for seq, cue := range sortedCues {
			if cue.StartMs < 0 || cue.EndMs < cue.StartMs || cue.Text == "" {
				return fmt.Errorf("insertTextTrackSegmentsInTx: invalid cue (seq=%d start=%d end=%d text_len=%d)",
					seq, cue.StartMs, cue.EndMs, len(cue.Text))
			}
			if _, execErr := stmt.ExecContext(ctx,
				trackID, seq+1, cue.StartMs, cue.EndMs, cue.Text,
			); execErr != nil {
				return fmt.Errorf("insertTextTrackSegmentsInTx: exec (seq=%d): %w", seq+1, execErr)
			}
		}
	}
	return nil
}

// ── Diagnostics ────────────────────────────────────────────────────

// MarshalCanonicalPayload marshals a map into a JSON raw message for
// callers that want to enrich the canonical payload with extra fields
// (e.g. metadata.summary, source_url). Exposed at the package boundary
// so callers don't need to import encoding/json directly.
func MarshalCanonicalPayload(extra map[string]any) (json.RawMessage, error) {
	if extra == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(extra)
}

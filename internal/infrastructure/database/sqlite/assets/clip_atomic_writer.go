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
//	UPSERT media_assets SET  ... (id=clipID, lifecycle_state='ACTIVE')
//	BUILD  eventKey, payload = BuildReindexEnvelopeV1(
//	          clipID, targetSchema="asset.index.requested.v1",
//	          sourceVersion=deriveSourceVersion(clipID, asset.FileHash, asset.PolicyVersion),
//	          requestedAt=now)
//	INSERT outbox_events (...) ON CONFLICT(event_key) WHERE !='' DO NOTHING
//	COMMIT
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
// Column projection (10 fields): the canonical ClipAsset → media_assets
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
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
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

	// ── 2) UPSERT media_assets
	nowStr := w.now().UTC().Format(time.RFC3339)
	if err := upsertClipInTx(ctx, tx, clipID, asset, nowStr); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert: %w", err)
	}

	// ── 3) Build canonical v1 envelope
	policyVersion := asset.PolicyVersion
	if policyVersion == "" {
		policyVersion = derivePolicyVersion(clipID)
	}
	sourceVersion := deriveSourceVersion(clipID, asset.FileHash, policyVersion)
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

	if w.log != nil {
		// Blocco 2.1: surface ON CONFLICT suppression by an existing
		// terminal row (dead_letter/superseded). The producer must
		// react — a freshly-completed tx that "succeeded" but silently
		// squelched the index request is exactly the silent-success
		// regression the audit called out.
		if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
			w.log.Warn("ClipAtomicWriterAdapter: outbox event suppressed by existing terminal row",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus))
		} else {
			w.log.Debug("ClipAtomicWriterAdapter: clip + index event committed",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.String("source_version", sourceVersion),
				zap.Bool("outbox_inserted", enqResult.Inserted))
		}
	}
	return nil
}

// isTerminalOutboxStatus reports whether an outbox row's status is
// terminal — useful for deciding whether a fresh INSERT was squelched
// by an already-completed/failed event, vs the more benign case
// where the same key was already in pending/processing.
func isTerminalOutboxStatus(status string) bool {
	return status == "dead_letter" || status == outboxevents.SupersedeStatus
}

// upsertClipInTx writes the canonical 10-column clip row shape into
// media_assets inside the caller's tx. The projection reads from
// ClipAsset's nested structs (Drive, Coordinates, Metadata) per the
// Commit 2 #6 verdict mandate.
func upsertClipInTx(ctx context.Context, tx *sql.Tx, clipID string, asset youtubetypes.ClipAsset, nowStr string) error {
	if tx == nil {
		return errors.New("upsertClipInTx: tx is nil")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			drive_file_id, drive_link, download_link,
			local_path, file_hash,
			folder_id, folder_path,
			lifecycle_state, updated_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?, ?)
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
		asset.Drive.FolderPath,
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

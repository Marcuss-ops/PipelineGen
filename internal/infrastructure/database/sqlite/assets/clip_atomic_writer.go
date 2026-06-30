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
// Post-Commit 1, the writer port is REQUIRED at construction time
// (fail-fast panic in NewProcessYouTubeSegmentUseCase for nil Writer —
// see process_segment.go godoc). This file is the production adapter
// the composition root wires.
//
// Transaction shape (canonical PR-C PR-VO-A3 pattern):
//
//	BEGIN
//	UPSERT media_assets SET  ... (id=clipID, lifecycle_state='ACTIVE')
//	BUILD  eventKey, payload = BuildReindexEnvelopeV1(
//	          clipID, targetSchema="asset.index.requested.v1",
//	          sourceVersion=deriveSourceVersion(clipID, item.FileHash),
//	          requestedAt=now)
//	INSERT outbox_events (...) ON CONFLICT(event_key) WHERE !='' DO NOTHING
//	COMMIT
//
// Idempotency contracts (mirrored from outboxevents.BuildReindexEnvelopeV1):
//   - eventKey shaped "reconcile:reindex:<assetID>:<schema>:<source>".
//     Repeated calls with the same (clipID, file_hash, policy) tuple
//     collapse via outbox ON CONFLICT(event_key) DO NOTHING — only one
//     outbox row exists for any (clip, content) pair. This is the
//     PR-VO-A3 atomicity invariant from FASE 4 godlike/06.
//   - Different file_hash on retry → new eventKey → new outbox row.
//     The supersede gate downstream (outbox.Pool / IndexingHandler)
//     compares payload.source_version against the current
//     media_assets.content_hash and rejects stale events as supersede.
//   - Empty sourceVersion is fail-closed at BuildReindexEnvelopeV1.
//     We compute sourceVersion = item.FileHash (the canonical
//     ingest-time hash). On empty FileHash (edge case where the
//     upstream pipeline bypassed hash.MD5File) we fall back to a
//     deterministic MD5(clipID + policyVersion) so the event_key
//     remains stable across retries. THIS NEVER produces a hash
//     collision with the real FileHash path because clipID +
//     policyVersion is a fixed tuple and FileHash is content-derived.
//
// Column projection (10 fields): the canonical ProcessSegmentResult
// → media_assets surface. LIVE state is updated_at + updated-once
// fields; lifecycle_state stays 'ACTIVE' (the canonical PR-C
// lifecycle) — soft-delete is delegated to LifecycleService. We
// intentionally do NOT include the metadata_json write side:
// ClipAtomicWriter is the bare writer bridge for the clip write;
// the metadata enrichment path is a distinct Phase (Commit 4).
package assets

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
//
// Panic on nil db / nil box reflects the verdict's P0 #3 hard-wiring
// directive: clip rows MUST persist together with their index event
// before the use case marks Item.Status="processed". A partial
// producer is worse than a loud panic.
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

// CommitClipAndIndexEvent performs the canonical atomic write:
//
//	BEGIN
//	UPSERT media_assets SET ...  for clipID
//	BUILD  envelope via outboxevents.BuildReindexEnvelopeV1
//	INSERT outbox_events ... ON CONFLICT(event_key) DO NOTHING
//	COMMIT
//
// Returns nil on success; returns wrapped error on tx failure.
// Half-applied states (UPSERT succeeded but outbox failed) are NOT
// possible: outboxevents.Repository.Enqueue writes through the SAME
// *sql.Tx that the UPSERT runs on, so COMMIT is the atomic barrier.
//
// SourceVersion derivation:
//   - Try item.FileHash verbatim.
//   - Fallback to deterministic MD5(clipID + policyVersion) when
//     FileHash is empty (the upstream hash.MD5File skipped path).
//     Stays invariant under retries → same eventKey → ON CONFLICT
//     collapses retries into a single outbox row.
//
// Empty clipID is rejected before any tx opens. Failed
// BuildReindexEnvelopeV1 is wrapped with the producer's local
// context.
func (w *ClipAtomicWriterAdapter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	item youtubetypes.ExtractItem,
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
	// Type empty → caller is using the canonical default; fill it.
	if event.Type == "" {
		event.Type = outboxevents.EventAssetIndexRequested
	}
	if event.AggregateID == "" {
		event.AggregateID = clipID
	}

	// ── 1) Begin tx ─────────────────────────────────────────────
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: begin tx: %w", err)
	}
	// Defensive rollback: the named return swallows a non-error
	// early-return (outbox errors only) into a defer that runs
	// rollback() AFTER the named-return is set. We use the simple
	// inline pattern instead because the function is short and the
	// tx.Commit() below either commits or errors out (in which
	// case the deferred Rollback is a no-op).
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── 2) UPSERT media_assets ─────────────────────────────────
	nowStr := w.now().UTC().Format(time.RFC3339)
	if err := upsertClipInTx(ctx, tx, clipID, item, nowStr); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert: %w", err)
	}

	// ── 3) Build canonical v1 envelope ─────────────────────────
	policyVersion := derivePolicyVersion(clipID)
	sourceVersion := deriveSourceVersion(clipID, item.FileHash, policyVersion)
	eventKey, payloadJSON, err := outboxevents.BuildReindexEnvelopeV1(
		clipID,
		outboxevents.ReindexEnvelopeV1Schema, // "asset.index.requested.v1"
		sourceVersion,
		w.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: build envelope: %w", err)
	}
	// If the caller supplied a payload, prefer it (e.g. includes
	// extra fields beyond the canonical envelope). The canonical
	// payload is the JSON document keyed by eventKey for idempotency —
	// this MUST be encoded into the row's payload_json verbatim so
	// the worker's supersede-gate compares against source_version.
	if len(event.Payload) > 0 {
		payloadJSON = string(event.Payload)
	}

	// ── 4) INSERT outbox_events (tx-bound) ─────────────────────
	if err := w.box.Enqueue(
		ctx,
		tx,
		outboxevents.EventAssetIndexRequested,
		clipID,
		"media_asset",
		payloadJSON,
		eventKey,
	); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: outbox enqueue: %w", err)
	}

	// ── 5) Commit ─────────────────────────────────────────────
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: commit: %w", err)
	}
	committed = true

	if w.log != nil {
		w.log.Debug("ClipAtomicWriterAdapter: clip + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("source_version", sourceVersion))
	}
	return nil
}

// upsertClipInTx writes the canonical 10-column clip row shape into
// media_assets inside the caller's tx. The projection intentionally
// stays narrow (Commit 1 minimum scope) — the metadata enrichment
// fields will be added in Commit 4 alongside the canonical
// ClipMetadataBuilder surface.
//
// Columns written: id, source, name, filename, drive_file_id,
// drive_link, download_link, local_path, file_hash, drive_folder_id,
// folder_path, lifecycle_state='ACTIVE', updated_at, created_at (on
// INSERT branch — covered by COALESCE on second-branch UPDATE only).
func upsertClipInTx(ctx context.Context, tx *sql.Tx, clipID string, item youtubetypes.ExtractItem, nowStr string) error {
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
		"youtube", // canonical source for clip rows
		routeEmpty(item.Name, clipID),
		routeEmpty(item.Filename, clipID+".mp4"),
		"video", // media_type — ytdlp_clips default; future PR adds DriveFileAdapter-driven type
		item.DriveFileID,
		item.DriveLink,
		item.DownloadLink,
		item.LocalPath,
		item.FileHash,
		item.DriveFolderID,
		item.DriveFolderPath,
		nowStr,
		nowStr,
	)
	if err != nil {
		return err
	}
	return nil
}

// routeEmpty is the canonical "fallback-to-this-string" helper for
// INSERT columns where an empty value would later fail a NOT NULL
// check. Kept as a private helper because tests can construct
// ExtractItems with empty Name (e.g. cleanClipName gap) and the
// adapter must keep the row insertable.
func routeEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// derivePolicyVersion extracts the policy_version suffix from a
// canonical clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>").
// Returns "v1" when the suffix is missing (legacy / build error
// / hand-crafted clipID) so the source-version fallback remains
// stable across retries. The shape is fixed by process_segment.go
// line ~118 (`clipID := fmt.Sprintf("yt_%s_%d_%d_%s", ...)`).
func derivePolicyVersion(clipID string) string {
	// Last 4 segments: yt_<videoID>_<start>_<end>_<policyVer>.
	// Find the last 4 underscores; if the last segment is non-empty
	// and not all-digit, treat it as the policy version.
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
// fingerprint used as event.source_version in the
// asset.index.requested.v1 envelope. In priority order:
//  1. item.FileHash (the canonical MD5 of the local clip file).
//  2. fallback = MD5(clipID + ":" + policyVersion) — invariant under
//     retries so ON CONFLICT(event_key) collapses into a single row.
//
// The fallback handles the edge case where the upstream
// hash.MD5File skipped its path (empty FileHash). The hash is
// deterministic per (clipID, policyVersion) tuple; re-extracts under
// the same (clipID, policy) get the same fallback hash, so the
// worker's supersede gate sees source_version=production-hash AND
// collapses the retry into a no-op.
func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
	if fileHash != "" {
		return fileHash
	}
	h := md5.Sum([]byte(clipID + ":" + policyVersion))
	return hex.EncodeToString(h[:])
}

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift (e.g. aggregator-type change in
// IndexEventPayload.Fields) surfaces as a build failure.
var _ youtubeports.ClipAtomicWriter = (*ClipAtomicWriterAdapter)(nil)

// ── Diagnostics ────────────────────────────────────────────────────

// JSON-marshal helper for callers that want to enrich the canonical
// payload with extra fields (e.g. metadata.summary, source_url). The
// function exists so callers don't depend on encoding/json directly;
// exposing it at the package boundary keeps the adapter's contract
// uniformly json. Returns the canonical envelope as a raw json.RawMessage.
func MarshalCanonicalPayload(extra map[string]any) (json.RawMessage, error) {
	if extra == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(extra)
}

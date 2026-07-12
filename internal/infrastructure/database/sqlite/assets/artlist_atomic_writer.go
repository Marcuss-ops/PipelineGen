// Package assets — artlist_atomic_writer.go (Fase 11 / Commit 1, July 2026):
// the canonical SINGLE-transaction wrap for the artlist publish finalization
// surface (DoD §16 finalizzazione transazionale + DoD §19 outbox).
//
// Invariant (godlike/07 NO-FAKE-AVAILABILITY):
//
//	Mai PUBLISHED senza outbox enqueuato nello stesso tx.
//
// The wrap guarantees this naturally via SQLite tx semantics:
//
//	1. BeginTx                    ← orchestrator
//	2. UPDATE media_assets SET
//	       drive_file_id, drive_link, download_link,
//	       file_hash, source_version,
//	       lifecycle_state='PUBLISHED',
//	       audit fields
//	     WHERE id = cmd.AssetID
//	   → if the row doesn't exist OR the UPDATE fails, the
//	   defer-Rollback below reverts the (still-uncommitted)
//	   UPDATE. The PUBLISHED transition NEVER escapes the
//	   rollback boundary.
//	3. BUILD eventKey, payload = OutboxKey("asset.index.requested.v1",
//	                                       "artlist", assetID, sourceVersion)
//	                          + artlistPublishRequestV1 JSON
//	4. INSERT outbox_events (...) ON CONFLICT(event_key) DO NOTHING
//	   → if the INSERT fails (DB error, UNIQUE violation on
//	   event_key, etc.), the defer-Rollback reverts BOTH the
//	   UPDATE in step 2 and the (uncommitted) outbox INSERT.
//	5. Commit                     ← orchestrator
//	6. checkOutboxTerminalAfterCommit  ← post-commit BLOCKER #4 check
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// artlist publish finalization surface. The artlist provider MUST
// route through CommitArtlistPublishTx; ad-hoc split-writes
// (UPDATE media_assets + INSERT outbox_events as two separate
// transactions) are the canonical godlike/06 violation that this
// commit closes.
//
// Transaction shape (mirrors clip_atomic_writer.go::CommitClipAndIndexEvent):
//
//	BEGIN
//	UPDATE media_assets ... WHERE id = ?
//	BUILD  eventKey, payload = (OutboxKey + artlistPublishRequestV1)
//	INSERT outbox_events ... ON CONFLICT(event_key) DO NOTHING
//	COMMIT
//
// godlike/07 typed-error contract: every abort path returns a
// typed error. The caller (artlist publish use case) can surface
// these errors verbatim — no silent success on half-applied
// writes. The post-commit BLOCKER #4 check (terminal outbox
// status) propagates a typed sentinel via
// `errors.Is(err, youtubeports.ErrOutboxTerminalConflict)`.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Typed sentinels (godlike/07 NO-FAKE-AVAILABILITY) ──────────────
//
// Every sentinel carries the canonical "no fake availability" reason
// text for grep-ability. The validate* helper + the in-tx helpers
// return these directly; the orchestrator wraps with the adapter's
// method name for operator triage. errors.Is dispatch is intentional
// (any *errorString match via the stdlib default).
var (
	errArtlistEmptyAssetID       = errors.New("artlist publish: asset_id is required (godlike/07 — no fake availability)")
	errArtlistEmptyAssetVersion  = errors.New("artlist publish: asset_version is required (godlike/07 — no fake availability)")
	errArtlistEmptyAssetLocation = errors.New("artlist publish: asset_location is required (godlike/07 — no fake availability)")
	errArtlistEmptyRendition     = errors.New("artlist publish: rendition is required (godlike/07 — no fake availability)")
	errArtlistEmptyDriveFileID   = errors.New("artlist publish: drive_file_id is required (godlike/07 — no fake availability)")
	errArtlistEmptyDriveLink     = errors.New("artlist publish: drive_link is required (godlike/07 — no fake availability)")
	errArtlistEmptyDownloadLink  = errors.New("artlist publish: download_link is required (godlike/07 — no fake availability)")
	errArtlistEmptyFileHash      = errors.New("artlist publish: file_hash is required (godlike/07 — supersede gate requires a fingerprint)")
	errArtlistEmptySourceVersion = errors.New("artlist publish: source_version is required (godlike/07 — supersede gate requires a fingerprint)")
	errArtlistEmptyEventKey      = errors.New("artlist publish: event_key is required (internal — derived from OutboxKey; empty implies a programming error in the orchestrator)")
	errArtlistEmptyRequestedAt   = errors.New("artlist publish: requested_at is required (internal — derived from now; empty implies a programming error in the orchestrator)")
	errArtlistAssetRowNotFound   = errors.New("artlist publish: no media_assets row with the given id (caller MUST pre-insert the staging row before calling CommitArtlistPublishTx)")
)

// artlistPublishTxAdapter implements the canonical atomic-write
// surface for artlist publish finalization. Holds *sql.DB (the
// ledger connection) and *outboxevents.Repository (the outbox
// writer — talks into the SAME connection within the same tx per
// the outboxevents.Repository.Enqueue contract).
//
// godlike/06 SSOT: the same package as ClipAtomicWriterAdapter so
// it can reuse the tx-bound outbox helper
// (enqueueClipIndexEventInTx) and the post-commit BLOCKER #4 check
// (checkOutboxTerminalAfterCommit) — both are package-private
// helpers in clip_atomic_writer_outbox.go. A new package would
// duplicate the helpers and break the cross-source canonical
// ownership of the tx-bound outbox INSERT.
type artlistPublishTxAdapter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	now func() time.Time // injectable clock for tests; production = time.Now
}

// ArtlistPublishCommand is the canonical DTO for the artlist
// publish finalization surface (Fase 11 / Commit 1). Every field
// is required (godlike/07 no-fake-availability) — the wrap fails
// closed on empty/missing values.
//
// Field mapping (user spec literal — July 2026):
//
//	"asset ID, asset version, asset location, rendition,
//	 Drive file ID/link, download link, file hash,
//	 source version, lifecycle=PUBLISHED, audit fields"
//
// Lifecycle is hard-coded to "PUBLISHED" by the wrap — the caller
// MUST NOT supply a different value. A pre-PUBLISHED row that
// needs a non-PUBLISHED transition uses a different surface
// (lifecycle.Service), not this wrap.
type ArtlistPublishCommand struct {
	// AssetIdentity
	AssetID      string // media_assets.id primary key (required)
	AssetVersion string // asset version (e.g. "v1") — required

	// Location + Rendition
	AssetLocation string // local path or remote URL — required
	Rendition     string // rendition identifier (e.g. "1080p") — required

	// Drive State
	DriveFileID  string // Google Drive file ID — required
	DriveLink    string // webViewLink (open in browser) — required
	DownloadLink string // signed download URL — required

	// Content fingerprint
	FileHash string // SHA-256 of the file bytes — required (godlike/07)

	// Provenance
	SourceVersion string // source_version (CAS fence for supersede gate) — required

	// Audit
	// The wrap sets created_at = COALESCE(NULLIF(created_at, ''), now)
	// (preserve the original insert time) and updated_at = now.
	// Pre-PUBLISHED created_at is preserved; updated_at always advances.
}

// newArtlistPublishTxAdapter constructs the adapter. Both db AND
// box MUST be non-nil — a nil either side is a fail-closed panic
// so a wiring gap lands in a build-side output rather than a
// runtime panic at first CommitArtlistPublishTx call.
func newArtlistPublishTxAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *artlistPublishTxAdapter {
	if db == nil {
		panic("assets.newArtlistPublishTxAdapter: db is required (composition must pass root.DB.DB)")
	}
	if box == nil {
		panic("assets.newArtlistPublishTxAdapter: outboxevents.Repository is required (composition must pass root.Outbox.EventsRepo)")
	}
	return &artlistPublishTxAdapter{
		db:  db,
		box: box,
		log: log,
		now: time.Now,
	}
}

// NewArtlistPublishTxAdapter is the EXPORTED constructor
// (composition root wiring). The exported alias keeps the
// panic-on-nil guard visible to the composition root audit
// (godlike/07) while reserving the unexported form for future
// test-only constructors that may want to skip the panic.
func NewArtlistPublishTxAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *artlistPublishTxAdapter {
	return newArtlistPublishTxAdapter(db, box, log)
}

// CommitArtlistPublishTx performs the canonical atomic write for
// artlist publish finalization.
//
// Step ordering (mirrors clip_atomic_writer.go::CommitClipAndIndexEvent):
//
//  1. BeginTx
//  2. updateArtlistAssetInTx  ← media_assets UPDATE (lifecycle=PUBLISHED)
//  3. Build eventKey via idempotency.OutboxKey + payload via artlistPublishRequestV1
//  4. enqueueClipIndexEventInTx  ← outbox_events INSERT (tx-bound)
//  5. Commit
//  6. checkOutboxTerminalAfterCommit  ← post-commit BLOCKER #4 typed-error
//
// godlike/06 SSOT: this is the SOLE canonical atomic surface for
// artlist publish finalization. The artlist provider's publish
// use case MUST route through this method; ad-hoc split-writes
// (UPDATE media_assets + INSERT outbox_events as two separate
// transactions) violate the godlike/06 SSOT and break the
// "Mai PUBLISHED senza outbox" invariant under partial failure.
func (a *artlistPublishTxAdapter) CommitArtlistPublishTx(ctx context.Context, cmd ArtlistPublishCommand) error {
	if a == nil || a.db == nil || a.box == nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: adapter not wired")
	}
	if err := validateArtlistPublishCommand(cmd); err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: %w", err)
	}

	// ── 1) Begin tx (orchestrator-owned).
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── 2) UPDATE media_assets (lifecycle_state='PUBLISHED' + the
	//     user-spec field set: asset_version, asset_location,
	//     rendition, drive_file_id, drive_link, download_link,
	//     file_hash, source_version, audit fields).
	nowStr := a.now().UTC().Format(time.RFC3339)
	if uerr := updateArtlistAssetInTx(ctx, tx, cmd, nowStr); uerr != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: update media_assets: %w", uerr)
	}

	// ── 3) Build event_key + payload (no SQL).
	eventKey, err := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, // "asset.index.requested.v1"
		"artlist",                            // source provider
		cmd.AssetID,                          // clip_id (asset_id)
		cmd.SourceVersion,                    // source_version
	)
	if err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: build event_key: %w", err)
	}
	payload, err := buildArtlistPublishRequestV1(cmd, eventKey, nowStr)
	if err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: build payload: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: marshal payload: %w", err)
	}

	// ── 4) INSERT outbox_events (tx-bound helper).
	enqResult, err := enqueueClipIndexEventInTx(
		ctx, a.box, tx,
		outboxevents.EventAssetIndexRequested, // event type
		cmd.AssetID,                          // aggregate_id
		cmd.AssetID,                          // asset_id (the inner id; same as aggregate for media_asset)
		string(payloadJSON),
		eventKey,
	)
	if err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: outbox enqueue: %w", err)
	}

	// ── 5) Commit (orchestrator-owned).
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: commit: %w", cerr)
	}
	committed = true

	// ── 6) BLOCKER #4 closure: terminal conflict → typed error, not silent success.
	if terr := checkOutboxTerminalAfterCommit(a.log, enqResult, cmd.AssetID, eventKey); terr != nil {
		return terr
	}

	if a.log != nil {
		a.log.Debug("artlistPublishTxAdapter: artlist asset + index event committed atomically",
			zap.String("asset_id", cmd.AssetID),
			zap.String("event_key", eventKey),
			zap.String("source_version", cmd.SourceVersion),
			zap.Bool("outbox_inserted", enqResult.Inserted),
		)
	}
	return nil
}

// validateArtlistPublishCommand enforces the godlike/07
// no-fake-availability contract: every field the user spec
// requires for the PUBLISHED transition MUST be non-empty. The
// wrap fails closed on empty/missing values.
//
// godlike/07 typed-error contract: empty fields are rejected
// BEFORE the tx opens. The caller (artlist publish use case) can
// surface the typed error verbatim — no silent half-applied
// writes.
func validateArtlistPublishCommand(cmd ArtlistPublishCommand) error {
	if cmd.AssetID == "" {
		return errArtlistEmptyAssetID
	}
	if cmd.AssetVersion == "" {
		return errArtlistEmptyAssetVersion
	}
	if cmd.AssetLocation == "" {
		return errArtlistEmptyAssetLocation
	}
	if cmd.Rendition == "" {
		return errArtlistEmptyRendition
	}
	if cmd.DriveFileID == "" {
		return errArtlistEmptyDriveFileID
	}
	if cmd.DriveLink == "" {
		return errArtlistEmptyDriveLink
	}
	if cmd.DownloadLink == "" {
		return errArtlistEmptyDownloadLink
	}
	if cmd.FileHash == "" {
		return errArtlistEmptyFileHash
	}
	if cmd.SourceVersion == "" {
		return errArtlistEmptySourceVersion
	}
	return nil
}

// updateArtlistAssetInTx is the tx-bound media_assets UPDATE
// helper. Sets the user-spec field set + lifecycle_state='PUBLISHED'
// + audit fields (created_at preserved via COALESCE,
// updated_at=now).
//
// godlike/06 SSOT: this helper is the SOLE place where the artlist
// publish UPDATE pattern lives. The orchestrator (CommitArtlistPublishTx)
// opens the tx; this helper accepts *sql.Tx as a parameter and
// NEVER opens its own transaction. Adding a BeginTx call here
// would shatter the atomic surface.
//
// Fail-closed contract: if the row does not exist
// (RowsAffected == 0), the helper returns a typed sentinel so
// the orchestrator propagates the failure UP to the defer
// Rollback. The PUBLISHED transition never escapes to a row
// that doesn't exist.
//
// godlike/07 audit-field preservation: created_at uses
// COALESCE(NULLIF(created_at, ''), ?) so a pre-existing insert
// timestamp is preserved (the staging row is pre-inserted by the
// drive publisher or the discovery flow; this wrap is the
// terminal "promote to PUBLISHED" step, NOT the initial insert).
func updateArtlistAssetInTx(ctx context.Context, tx *sql.Tx, cmd ArtlistPublishCommand, nowStr string) error {
	if tx == nil {
		return fmt.Errorf("updateArtlistAssetInTx: tx is nil")
	}
	const query = `UPDATE media_assets
		SET source = 'artlist',
		    asset_version = ?,
		    asset_location = ?,
		    rendition = ?,
		    drive_file_id = ?,
		    drive_link = ?,
		    download_link = ?,
		    file_hash = ?,
		    source_version = ?,
		    lifecycle_state = 'PUBLISHED',
		    created_at = COALESCE(NULLIF(created_at, ''), ?),
		    updated_at = ?
		WHERE id = ?`
	res, err := tx.ExecContext(ctx, query,
		cmd.AssetVersion,
		cmd.AssetLocation,
		cmd.Rendition,
		cmd.DriveFileID,
		cmd.DriveLink,
		cmd.DownloadLink,
		cmd.FileHash,
		cmd.SourceVersion,
		nowStr,
		nowStr,
		cmd.AssetID,
	)
	if err != nil {
		return fmt.Errorf("updateArtlistAssetInTx: exec: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("updateArtlistAssetInTx: rows affected: %w", err)
	}
	if n == 0 {
		return errArtlistAssetRowNotFound
	}
	return nil
}

// artlistPublishRequestV1 is the canonical envelope for the
// asset.index.requested.v1 event emitted by the artlist publish
// finalization surface. It extends the canonical
// outboxevents.indexRequestV1 with the user-spec-required
// source/media_type/lifecycle_state/file_hash fields.
//
// godlike/06 SSOT: this struct is the SOLE canonical owner of
// the artlist publish envelope shape. Other sources (YouTube,
// Stock, Voiceover) emit their own per-source supersets; the
// IndexingHandler consumer reads the canonical fields
// (asset_id, source_version, source, media_type, file_hash,
// lifecycle_state, idempotency_key) across all sources.
type artlistPublishRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Operation      string `json:"operation"`
	SourceVersion  string `json:"source_version"`
	Source         string `json:"source"`
	MediaType      string `json:"media_type"`
	FileHash       string `json:"file_hash"`
	LifecycleState string `json:"lifecycle_state"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestedAt    string `json:"requested_at"`
}

// buildArtlistPublishRequestV1 constructs the canonical envelope.
// Pure data transformation (no SQL, no IO) — easy to unit-test
// without a database.
func buildArtlistPublishRequestV1(cmd ArtlistPublishCommand, eventKey, nowStr string) (artlistPublishRequestV1, error) {
	if eventKey == "" {
		return artlistPublishRequestV1{}, errArtlistEmptyEventKey
	}
	if nowStr == "" {
		return artlistPublishRequestV1{}, errArtlistEmptyRequestedAt
	}
	return artlistPublishRequestV1{
		SchemaVersion:  "asset.index.requested.v1",
		EventID:        uuid.NewString(),
		AssetID:        cmd.AssetID,
		Operation:      "publish",
		SourceVersion:  cmd.SourceVersion,
		Source:         "artlist",
		MediaType:      "video",
		FileHash:       cmd.FileHash,
		LifecycleState: "PUBLISHED",
		IdempotencyKey: eventKey,
		RequestedAt:    nowStr,
	}, nil
}

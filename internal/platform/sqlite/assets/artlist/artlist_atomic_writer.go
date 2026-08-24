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
//  1. Build CommitRequest from ArtlistPublishCommand.
//  2. Delegate to persistence.AssetCommitter.CommitAndIndex.
//     AssetCommitter atomically writes media_assets, asset_locations,
//     typed metadata, and the asset.index.requested outbox event.
//  3. Surface any typed error verbatim.
//
// godlike/06 SSOT: this file is now a thin source-specific mapper over
// the canonical AssetCommitter. The committer is the SOLE place that
// performs the actual INSERT/UPSERT into media_assets,
// asset_locations, and outbox_events.
package artlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Typed sentinels (godlike/07 NO-FAKE-AVAILABILITY) ──────────────
//
// Every sentinel carries the canonical "no fake availability" reason
// text for grep-ability. The validate* helper returns these directly;
// the orchestrator wraps with the adapter's method name for operator
// triage. errors.Is dispatch is intentional (any *errorString match
// via the stdlib default).
var (
	errArtlistEmptyAssetID       = errors.New("artlist publish: asset_id is required (godlike/07 — no fake availability)")
	errArtlistEmptyAssetVersion  = errors.New("artlist publish: asset_version is required (godlike/07 — no fake availability)")
	errArtlistEmptyAssetLocation = errors.New("artlist publish: asset_location is required (godlike/07 — no fake availability)")
	errArtlistEmptyRendition     = errors.New("artlist publish: rendition is required (godlike/07 — no fake availability)")
	errArtlistEmptyDriveFileID   = errors.New("artlist publish: drive_file_id is required (godlike/07 — no fake availability)")
	errArtlistEmptyDriveLink     = errors.New("artlist publish: drive_link is required (godlike/07 — no fake availability)")
	errArtlistEmptyDownloadLink  = errors.New("artlist publish: download_link is required (godlike/07 — no fake availability)")
	errArtlistEmptyLegacyFileMD5 = errors.New("artlist publish: file_hash is required (godlike/07 — supersede gate requires a fingerprint)")
	errArtlistEmptySourceVersion = errors.New("artlist publish: source_version is required (godlike/07 — supersede gate requires a fingerprint)")
)

// artlistPublishTxAdapter implements the canonical atomic-write
// surface for artlist publish finalization. Holds *sql.DB (the
// ledger connection) and *outboxevents.Repository (the outbox
// writer — talks into the SAME connection within the same tx per
// the outboxevents.Repository.Enqueue contract).
//
// godlike/06 SSOT: the adapter now delegates all durable writes to
// persistence.AssetCommitter. This file only owns the artlist-specific
// command validation and mapping.
type artlistPublishTxAdapter struct {
	committer persistence.AssetCommitter
	log       *zap.Logger
	now       func() time.Time // injectable clock for tests; production = time.Now
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
	LegacyFileMD5 string // SHA-256 of the file bytes — required (godlike/07)

	// Provenance
	SourceVersion string // source_version (CAS fence for supersede gate) — required

	// Audit
	// The wrap sets created_at = COALESCE(NULLIF(created_at, ''), now)
	// (preserve the original insert time) and updated_at = now.
	// Pre-PUBLISHED created_at is preserved; updated_at always advances.
}

// newArtlistPublishTxAdapter constructs the adapter. Both db AND
// box MUST be non-nil — a nil either side is a fail-closed panic
// so a wiring gap lands at build/test time rather than a
// runtime panic at first CommitArtlistPublishTx call.
func newArtlistPublishTxAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *artlistPublishTxAdapter {
	if db == nil {
		panic("assets.newArtlistPublishTxAdapter: db is required (composition must pass root.DB.DB)")
	}
	if box == nil {
		panic("assets.newArtlistPublishTxAdapter: outboxevents.Repository is required (composition must pass root.Outbox.EventsRepo)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &artlistPublishTxAdapter{
		committer: assets.NewSQLiteAssetCommitter(db, box, log),
		log:       log,
		now:       time.Now,
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
// Step ordering:
//
//  1. Validate command.
//  2. Build persistence.CommitRequest.
//  3. Delegate to AssetCommitter.CommitAndIndex.
//
// godlike/06 SSOT: this is now a thin mapper over the canonical
// AssetCommitter. The actual SQL lives in one place.
func (a *artlistPublishTxAdapter) CommitArtlistPublishTx(ctx context.Context, cmd ArtlistPublishCommand) error {
	if a == nil || a.committer == nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: adapter not wired")
	}
	if err := validateArtlistPublishCommand(cmd); err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: %w", err)
	}

	req := persistence.CommitRequest{
		AssetID:        cmd.AssetID,
		Source:         "artlist",
		Filename:       cmd.AssetID,
		MediaType:      "video",
		ContentHash:    cmd.LegacyFileMD5,
		LifecycleState: "PUBLISHED",
		IndexState:     "DISCOVERED",
		AssetVersion:   cmd.AssetVersion,
		AssetLocation:  cmd.AssetLocation,
		Rendition:      cmd.Rendition,
		Metadata: persistence.TypedMetadata{
			SourceVersion: cmd.SourceVersion,
		},
		Locations: []persistence.LocationCommit{
			{
				Kind:        "drive",
				Provider:    "drive",
				ExternalID:  cmd.DriveFileID,
				URI:         cmd.AssetLocation,
				WebViewLink: cmd.DriveLink,
				DownloadURL: cmd.DownloadLink,
				IsPrimary:   true,
			},
		},
		EmitIndexEvent: true,
		RequestedAt:    a.now(),
	}

	if _, err := a.committer.CommitAndIndex(ctx, req); err != nil {
		return fmt.Errorf("artlistPublishTxAdapter.CommitArtlistPublishTx: %w", err)
	}

	if a.log != nil {
		a.log.Debug("artlistPublishTxAdapter: artlist asset + index event committed atomically",
			zap.String("asset_id", cmd.AssetID),
			zap.String("source_version", cmd.SourceVersion),
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
	if cmd.LegacyFileMD5 == "" {
		return errArtlistEmptyLegacyFileMD5
	}
	if cmd.SourceVersion == "" {
		return errArtlistEmptySourceVersion
	}
	return nil
}

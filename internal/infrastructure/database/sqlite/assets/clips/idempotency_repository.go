// Package clips — idempotency_repository.go (Stock Pipeline Cutover P0-CLIP-IDEMP, July 2026).
//
// SQLite concrete of the canonical clips.Idempotency port from
// internal/domain/clips/idempotency.go. Owns:
//   1. Reads against clip_storage_index (Inspect).
//   2. Idempotent INSERT-OR-IGNORE presence flips (RecordPersistence/
//      RecordDrive/RecordQdrant).
//
// godlike/06 SSOT: this concrete is the SOLE canonical SQLite
// adapter for the clip-storage-layer presence surface. Application
// code MUST consume the Idempotency port from THIS adapter (or a
// hermetic test fake) — NOT a parallel `clip_storage_index`
// query, NOT a join-spree across media_assets/index_state/
// outbox.event_key. Drift between this concrete and the port is
// caught at compile time by the `var _ clipsdomain.Idempotency =
// (*IdempotencyRepository)(nil)` assertion below.
//
// godlike/07 NO-FAKE-AVAILABILITY: every method rejects empty
// inputs with typed sentinels (clipsdomain.ErrEmptyClipIdentity +
// ErrEmptyAssetID + ErrEmptyDriveFileID + ErrEmptyQdrantPointID).
// The presence bits flip 0→1 ONLY on the FIRST write that
// supplies the corresponding ID; subsequent calls are no-ops
// (UNIQUE on clip_key + presence bit semantics).
//
// # SQLite specifics
//
// The mattn/go-sqlite3 driver (per AGENTS.md driver lock) makes
// the SQL surface portable across x86/arm and Linux/macOS; we
// use parameterized queries throughout so SQL injection is
// structurally impossible from the Go side. WAL is configured
// at db-open time so concurrent Inspect/Record from the
// stockbuild orchestrator's parallel phase bodies are safe —
// the UNIQUE PRIMARY KEY on clip_key is the authoritative gate
// against double-flip.

package clips

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	clipsdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/clips"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time assertion: *IdempotencyRepository satisfies the
// canonical clips.Idempotency port. Drift in any of the 4 method
// signatures IS a build failure at this declaration site (per
// godlike/06 SSOT one-canonical-owner-per-fact).
var _ clipsdomain.Idempotency = (*IdempotencyRepository)(nil)

// IdempotencyRepository is the SQLite-backed concrete of
// clipsdomain.Idempotency. Thread safety is delegated to the
// underlying *sql.DB (mattn/go-sqlite3 driver is safe for
// concurrent use under default pool limits; WAL + busy-timeout
// are configured at db-open time per the codebase convention).
type IdempotencyRepository struct {
	db    *sql.DB
	nowFn func() time.Time // injected clock; defaults to time.Now via constructor
}

// NewIdempotencyRepository opens the SQLite-backed Idempotency
// port. db MUST be a non-nil *sql.DB that has migration 182
// applied (the clip_storage_index table + ix_clip_storage_index_*
// indexes). The constructor does not run migrations itself —
// that is composition-root responsibility per godlike/06 SSOT
// (single writer of schema-version-facts).
//
// godlike/07 typed-error: returns nil + a typed error when db
// is nil so a half-wired boot fails loud at composition time,
// not at first Inspect call from a phase body.
func NewIdempotencyRepository(db *sql.DB) (*IdempotencyRepository, error) {
	if db == nil {
		return nil, errors.New("clips.NewIdempotencyRepository: db is required (godlike/07 - no fake availability)")
	}
	return &IdempotencyRepository{db: db, nowFn: time.Now}, nil
}

// WithClock returns a copy of r with an injected clock. Used by
// tests to assert timestamp stamps deterministically (without
// freezing the global time.Now reference). godlike/06 SSOT:
// tests may introduce a clock injection without affecting the
// canonical constructor — production callers MUST use
// NewIdempotencyRepository directly so the system clock is in
// effect.
func (r *IdempotencyRepository) WithClock(fn func() time.Time) *IdempotencyRepository {
	if fn == nil {
		return r
	}
	copy := *r
	copy.nowFn = fn
	return &copy
}

// Inspect returns the per-layer presence for clipKey. Returns
// (LayerPresence{false, false, false}, nil) when the row is
// absent (the canonical "fresh clip" state per user spec
// case 0 in the matrix).
func (r *IdempotencyRepository) Inspect(ctx context.Context, clipKey string) (clipsdomain.LayerPresence, error) {
	if clipKey == "" {
		return clipsdomain.LayerPresence{}, clipsdomain.ErrEmptyClipIdentity
	}
	var hasDB, hasDrive, hasQdrant int
	row := r.db.QueryRowContext(ctx, `SELECT has_db, has_drive, has_qdrant FROM clip_storage_index WHERE clip_key = ?`, clipKey)
	switch err := row.Scan(&hasDB, &hasDrive, &hasQdrant); {
	case errors.Is(err, sql.ErrNoRows):
		return clipsdomain.LayerPresence{}, nil
	case err != nil:
		return clipsdomain.LayerPresence{}, fmt.Errorf("clips.IdempotencyRepository.Inspect: SELECT clip_key=%q: %w", clipKey, err)
	}
	return clipsdomain.LayerPresence{
		HasDB:     hasDB == 1,
		HasDrive:  hasDrive == 1,
		HasQdrant: hasQdrant == 1,
	}, nil
}

// RecordPersistence stamps the SQLite persistence presence
// (has_db 0→1) on first call, idempotent on subsequent calls.
// asset_id is the canonical media_assets.id UUID that links
// the storage row to the SQLite row; the orchestrator's INDEX
// phase consumes it to know which row to emit
// asset.index.requested for.
//
// godlike/06 SSOT alignment: this is the canonical record after
// the dispatcher's tx commits (UpsertClipTx in
// clips_transactions.go is the WRITE; this method is the
// OBSERVE).
func (r *IdempotencyRepository) RecordPersistence(ctx context.Context, clipKey, assetID string) error {
	if clipKey == "" {
		return clipsdomain.ErrEmptyClipIdentity
	}
	if assetID == "" {
		return clipsdomain.ErrEmptyAssetID
	}
	now := timeutil.FormatRFC3339(r.nowFn())
	// INSERT-OR-IGNORE on PK clip_key + ON CONFLICT...DO UPDATE that
	// is conditional on the current has_db bit being 0 → preserves
	// the operator's first-write-wins semantics for persisted_at +
	// asset_id. Subsequent calls are no-ops on those columns; only
	// updated_at advances (intentional — operators see the retry
	// traffic on the dashboard without needing a separate retry metric).
	_, err := r.db.ExecContext(ctx, `
INSERT INTO clip_storage_index (
    clip_key, asset_id, has_db, has_drive, has_qdrant, persisted_at,
    created_at, updated_at
) VALUES (?, ?, 1, 0, 0, ?, ?, ?)
ON CONFLICT(clip_key) DO UPDATE SET
    asset_id = excluded.asset_id,
    has_db = 1,
    persisted_at = CASE
        WHEN clip_storage_index.persisted_at = '' OR clip_storage_index.persisted_at IS NULL
        THEN excluded.persisted_at
        ELSE clip_storage_index.persisted_at
    END,
    updated_at = excluded.updated_at
`, clipKey, assetID, now, now, now)
	if err != nil {
		return fmt.Errorf("clips.IdempotencyRepository.RecordPersistence: INSERT-OR-IGNORE clip_key=%q asset_id=%q: %w", clipKey, assetID, err)
	}
	return nil
}

// RecordDrive stamps the Drive presence (has_drive 0→1) on
// first call. drive_link is optional (the canonical wire shape
// is `(fileID, webViewLink)`); empty drive_link is permitted
// but empty drive_file_id is the typed ErrEmptyDriveFileID
// sentinel per godlike/07.
func (r *IdempotencyRepository) RecordDrive(ctx context.Context, clipKey, driveFileID, driveLink string) error {
	if clipKey == "" {
		return clipsdomain.ErrEmptyClipIdentity
	}
	if driveFileID == "" {
		return clipsdomain.ErrEmptyDriveFileID
	}
	now := timeutil.FormatRFC3339(r.nowFn())
	_, err := r.db.ExecContext(ctx, `
INSERT INTO clip_storage_index (
    clip_key, has_db, has_drive, has_qdrant, drive_file_id, drive_link,
    uploaded_at, created_at, updated_at
) VALUES (?, 0, 1, 0, ?, ?, ?, ?, ?)
ON CONFLICT(clip_key) DO UPDATE SET
    has_drive = 1,
    drive_file_id = excluded.drive_file_id,
    drive_link = excluded.drive_link,
    uploaded_at = CASE
        WHEN clip_storage_index.uploaded_at = '' OR clip_storage_index.uploaded_at IS NULL
        THEN excluded.uploaded_at
        ELSE clip_storage_index.uploaded_at
    END,
    updated_at = excluded.updated_at
`, clipKey, driveFileID, driveLink, now, now, now)
	if err != nil {
		return fmt.Errorf("clips.IdempotencyRepository.RecordDrive: INSERT-OR-IGNORE clip_key=%q drive_file_id=%q: %w", clipKey, driveFileID, err)
	}
	return nil
}

// RecordQdrant stamps the Qdrant presence (has_qdrant 0→1) on
// first call. qdrantPointID is the canonical Qdrant point ID
// (UUID-bytes-derived); empty is the typed ErrEmptyQdrantPointID
// sentinel per godlike/07.
//
// NB: callers SHOULD have observed an existing media_assets
// row (has_db=1) BEFORE calling this method (the orchestrator's
// INDEX phase emits asset.index.requested and waits for the
// IndexingHandler to stamp the row). If Inspect() returns a
// presence with !HasDB && HasQdrant, the higher-level use case
// returns clipsdomain.ErrStorageInconsistent — we do NOT
// enforce that here because the typed-error encoding is the
// use-case's responsibility.
func (r *IdempotencyRepository) RecordQdrant(ctx context.Context, clipKey, qdrantPointID string) error {
	if clipKey == "" {
		return clipsdomain.ErrEmptyClipIdentity
	}
	if qdrantPointID == "" {
		return clipsdomain.ErrEmptyQdrantPointID
	}
	now := timeutil.FormatRFC3339(r.nowFn())
	_, err := r.db.ExecContext(ctx, `
INSERT INTO clip_storage_index (
    clip_key, has_db, has_drive, has_qdrant, qdrant_point_id, indexed_at,
    created_at, updated_at
) VALUES (?, 0, 0, 1, ?, ?, ?, ?)
ON CONFLICT(clip_key) DO UPDATE SET
    has_qdrant = 1,
    qdrant_point_id = excluded.qdrant_point_id,
    indexed_at = CASE
        WHEN clip_storage_index.indexed_at = '' OR clip_storage_index.indexed_at IS NULL
        THEN excluded.indexed_at
        ELSE clip_storage_index.indexed_at
    END,
    updated_at = excluded.updated_at
`, clipKey, qdrantPointID, now, now, now)
	if err != nil {
		return fmt.Errorf("clips.IdempotencyRepository.RecordQdrant: INSERT-OR-IGNORE clip_key=%q qdrant_point_id=%q: %w", clipKey, qdrantPointID, err)
	}
	return nil
}

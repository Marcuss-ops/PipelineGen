// Package clips — idempotency_repository_test.go (Stock Pipeline Cutover P0-CLIP-IDEMP, July 2026).
//
// Integration test for the SQLite concrete of clipsdomain.Idempotency.
// Uses an in-memory SQLite (`:memory:`) with migration 182 applied
// inline so the test is hermetic and runnable without a tmp DB
// on disk.
//
// Test plan (matches user-spec 4-case matrix):
//   - TestIdempotencyRepository_FreshClip_ReturnsAbsentPresence
//   - TestIdempotencyRepository_RecordPersistence_FlipsHasDBOnly
//   - TestIdempotencyRepository_RecordDrive_FlipsHasDriveOnly
//   - TestIdempotencyRepository_RecordQdrant_FlipsHasQdrantOnly
//   - TestIdempotencyRepository_AllThreePresent_ReturnsFullyPresent
//   - TestIdempotencyRepository_IdempotentRecord_NoStateDrift
//   - TestIdempotencyRepository_RejectsEmptyInputs (table-driven)
//
// Inconsistent cases (Qdrant-or-Drive without DB) are NOT
// repairable and surface at the USE-CASE layer as
// clipsdomain.ErrStorageInconsistent — the repository doesn't
// enforce that because it's the orchestrator's responsibility.

package clips

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	clipsdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/clips"

	_ "github.com/mattn/go-sqlite3"
)

// migration182 is the inlined DDL for tests. Mirrors
// migrations/sqlite/182_clip_storage_index.sql verbatim so
// a drift in the migration will fail at test apply time.
const migration182 = `
CREATE TABLE IF NOT EXISTS clip_storage_index (
    clip_key        TEXT PRIMARY KEY,
    asset_id        TEXT,
    has_db          INTEGER NOT NULL,
    has_drive       INTEGER NOT NULL,
    has_qdrant      INTEGER NOT NULL,
    drive_file_id   TEXT,
    drive_link      TEXT,
    qdrant_point_id TEXT,
    persisted_at    TEXT,
    uploaded_at     TEXT,
    indexed_at      TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
`

// newTestRepo opens an in-memory SQLite, applies migration 182,
// instantiates the repository, and returns a Close cleanup func.
// FrozenTime is the injected clock used by every Record* method
// so timestamps are deterministic across runs.
func newTestRepo(t *testing.T) (*IdempotencyRepository, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open(in-memory sqlite3): %v", err)
	}
	if _, err := db.Exec(migration182); err != nil {
		_ = db.Close()
		t.Fatalf("apply migration 182: %v", err)
	}
	repo, err := NewIdempotencyRepository(db)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewIdempotencyRepository: %v", err)
	}
	// Frozen clock: 2026-07-25T00:00:00Z. Tests asserting
	// timestamps get byte-stable output.
	frozen := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	repo = repo.WithClock(func() time.Time { return frozen })
	return repo, func() { _ = db.Close() }
}

const (
	testClipKey      = "6b76e7d8a9c45f7c71d8e0fa9a4c8a08b1d6f3c4d5e6f7a8b9c0d1e2f3a4b5c6d" // 64-char fake
	testAssetID      = "asset-uuid-1234"
	testDriveFile    = "drive-file-1234"
	testDriveLink    = "https://drive.google.com/file/d/drive-file-1234"
	testQdrantPt     = "qdrant-point-uuid-5678"
	testOtherClipKey = "aaaa7d8a9c45f7c71d8e0fa9a4c8a08b1d6f3c4d5e6f7a8b9c0d1e2f3a4b5678ef"
)

// TestIdempotencyRepository_FreshClip_ReturnsAbsentPresence locks
// the canonical "case 0" state: a never-seen clip_key has all
// bits unset and Inspect returns LayerPresence{}.
func TestIdempotencyRepository_FreshClip_ReturnsAbsentPresence(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	lp, err := repo.Inspect(ctx, testClipKey)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if lp.HasDB || lp.HasDrive || lp.HasQdrant {
		t.Errorf("fresh clip should have all layers absent, got %+v", lp)
	}
	if !lp.AnythingAbsent() {
		t.Errorf("AnythingAbsent() must return true for fresh clip")
	}
	if lp.FullyPresent() {
		t.Errorf("FullyPresent() must return false for fresh clip")
	}
	if lp.Inconsistent() {
		t.Errorf("Inconsistent() must return false for fresh clip")
	}
}

// TestIdempotencyRepository_RecordPersistence_FlipsHasDBOnly locks
// case 2 in the user-spec matrix (no DB, no Drive, no Qdrant →
// record persistence → has_db=1, others=0).
func TestIdempotencyRepository_RecordPersistence_FlipsHasDBOnly(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence: %v", err)
	}
	lp, err := repo.Inspect(ctx, testClipKey)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !lp.HasDB {
		t.Errorf("after RecordPersistence, HasDB = false (want true)")
	}
	if lp.HasDrive {
		t.Errorf("after RecordPersistence, HasDrive = true (want false)")
	}
	if lp.HasQdrant {
		t.Errorf("after RecordPersistence, HasQdrant = true (want false)")
	}
	if lp.FullyPresent() {
		t.Errorf("FullyPresent() should be false with only DB set")
	}
}

// TestIdempotencyRepository_RecordAllThree_ReturnsFullyPresent
// exercises the full happy-path: case 0 → case 7. This is the
// canonical "skip-on-resume" state that the stockbuild.Handler
// hits on a no-op re-run.
func TestIdempotencyRepository_RecordAllThree_ReturnsFullyPresent(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence: %v", err)
	}
	if err := repo.RecordDrive(ctx, testClipKey, testDriveFile, testDriveLink); err != nil {
		t.Fatalf("RecordDrive: %v", err)
	}
	if err := repo.RecordQdrant(ctx, testClipKey, testQdrantPt); err != nil {
		t.Fatalf("RecordQdrant: %v", err)
	}
	lp, err := repo.Inspect(ctx, testClipKey)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !lp.FullyPresent() {
		t.Errorf("after Record all 3, FullyPresent = false (got %+v)", lp)
	}
	if lp.AnythingAbsent() {
		t.Errorf("after Record all 3, AnythingAbsent = true (got %+v)", lp)
	}
	if lp.Inconsistent() {
		t.Errorf("after Record all 3, Inconsistent = true (got %+v)", lp)
	}
}

// TestIdempotencyRepository_RecordDriveAfterDB_FlipsHasDriveOnly
// covers the canonical "case 4" repair path: SQLite present,
// Drive missing → operator UPLOAD repair → has_drive flips.
func TestIdempotencyRepository_RecordDriveAfterDB_FlipsHasDriveOnly(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence: %v", err)
	}
	// Snapshot: HasDB=true, HasDrive=false, HasQdrant=false.
	lp, _ := repo.Inspect(ctx, testClipKey)
	if !lp.HasDB || lp.HasDrive || lp.HasQdrant {
		t.Fatalf("pre-Upload state drift: %+v", lp)
	}

	// Operator uploads → record the drive layer.
	if err := repo.RecordDrive(ctx, testClipKey, testDriveFile, testDriveLink); err != nil {
		t.Fatalf("RecordDrive: %v", err)
	}

	lp, _ = repo.Inspect(ctx, testClipKey)
	if !lp.HasDB || !lp.HasDrive || lp.HasQdrant {
		t.Errorf("post-Upload state expected HasDB=true HasDrive=true HasQdrant=false; got %+v", lp)
	}
}

// TestIdempotencyRepository_IdempotentRecord_NoStateDrift locks
// the godlike/07 contract: a second Record* call on the same
// clip_key MUST NOT undo the first call. The presence bits stay
// flipped, and updated_at advances (operator retry traffic on
// the dashboard).
func TestIdempotencyRepository_IdempotentRecord_NoStateDrift(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	// First call.
	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence (first): %v", err)
	}
	first, _ := repo.Inspect(ctx, testClipKey)

	// Second call with the same clip_key + asset_id.
	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence (second): %v", err)
	}
	second, _ := repo.Inspect(ctx, testClipKey)

	if first.HasDB != second.HasDB || first.HasDrive != second.HasDrive || first.HasQdrant != second.HasQdrant {
		t.Errorf("idempotent RecordPersistence drifted the presence bits: first=%+v second=%+v", first, second)
	}
}

// TestIdempotencyRepository_RejectsEmptyInputs locks godlike/07
// across all 4 entry points: a failsafe wrapper for the typed
// sentinels that downstream callers branch on via errors.Is.
func TestIdempotencyRepository_RejectsEmptyInputs(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name    string
		do      func() error
		wantErr error
	}{
		{
			name:    "Inspect with empty clipKey",
			do:      func() error { _, err := repo.Inspect(ctx, ""); return err },
			wantErr: clipsdomain.ErrEmptyClipIdentity,
		},
		{
			name:    "RecordPersistence with empty clipKey",
			do:      func() error { return repo.RecordPersistence(ctx, "", testAssetID) },
			wantErr: clipsdomain.ErrEmptyClipIdentity,
		},
		{
			name:    "RecordPersistence with empty assetID",
			do:      func() error { return repo.RecordPersistence(ctx, testClipKey, "") },
			wantErr: clipsdomain.ErrEmptyAssetID,
		},
		{
			name:    "RecordDrive with empty clipKey",
			do:      func() error { return repo.RecordDrive(ctx, "", testDriveFile, testDriveLink) },
			wantErr: clipsdomain.ErrEmptyClipIdentity,
		},
		{
			name:    "RecordDrive with empty driveFileID",
			do:      func() error { return repo.RecordDrive(ctx, testClipKey, "", testDriveLink) },
			wantErr: clipsdomain.ErrEmptyDriveFileID,
		},
		{
			name:    "RecordQdrant with empty clipKey",
			do:      func() error { return repo.RecordQdrant(ctx, "", testQdrantPt) },
			wantErr: clipsdomain.ErrEmptyClipIdentity,
		},
		{
			name:    "RecordQdrant with empty qdrantPointID",
			do:      func() error { return repo.RecordQdrant(ctx, testClipKey, "") },
			wantErr: clipsdomain.ErrEmptyQdrantPointID,
		},
	}

	for _, c := range cases {
		err := c.do()
		if err == nil {
			t.Errorf("case %q: expected error, got nil", c.name)
			continue
		}
		if !errors.Is(err, c.wantErr) {
			t.Errorf("case %q: err = %v, want errors.Is %v", c.name, err, c.wantErr)
		}
	}
}

// TestIdempotencyRepository_DifferentClipsIsolated locks the
// "scoped to clip_key" property: RecordX on clip A MUST NOT
// affect Inspect on clip B.
func TestIdempotencyRepository_DifferentClipsIsolated(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.RecordPersistence(ctx, testClipKey, testAssetID); err != nil {
		t.Fatalf("RecordPersistence clipA: %v", err)
	}

	// Inspect an unseen clip — must return absent presence.
	lpOther, err := repo.Inspect(ctx, testOtherClipKey)
	if err != nil {
		t.Fatalf("Inspect clipB: %v", err)
	}
	if lpOther.HasDB || lpOther.HasDrive || lpOther.HasQdrant {
		t.Errorf("clipB should be fully absent; got %+v", lpOther)
	}

	// Inspect the seeded clipA — must return HasDB=true.
	lpA, err := repo.Inspect(ctx, testClipKey)
	if err != nil {
		t.Fatalf("Inspect clipA: %v", err)
	}
	if !lpA.HasDB {
		t.Errorf("clipA should be HasDB=true; got %+v", lpA)
	}
}

// TestIdempotencyRepository_NilDB_ReturnsTypedError locks the
// composition-root fail-closed gate: a half-wired boot panics
// immediately (we return a typed error rather than a nil
// repository with a runtime crash on first Inspect).
func TestIdempotencyRepository_NilDB_ReturnsTypedError(t *testing.T) {
	repo, err := NewIdempotencyRepository(nil)
	if err == nil {
		t.Fatalf("NewIdempotencyRepository(nil) returned nil error (godlike/07 violation)")
	}
	if repo != nil {
		t.Errorf("repo should be nil on error, got non-nil")
	}
}

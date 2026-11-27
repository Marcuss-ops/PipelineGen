// Package sqlite — pool_test.go (FASE 6 Cut 6.2, July 2026).
//
// Minimal regression tests for the canonical DualPool surface
// (internal/platform/sqlite/pool.go) + walPragmas
// enrichment.

package sqlite

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── DualPool construction ────────────────────────────────────────────────

// TestNewDualPool_WALModeApplied pins the load-bearing pragma
// invariant: NewDualPool's enriched URI must include journal_mode(WAL).
func TestNewDualPool_WALModeApplied(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_wal.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()
	require.NotNil(t, pool.Writer)
	require.NotNil(t, pool.Reader)

	var journalMode string
	require.NoError(t, pool.Writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
	assert.Equal(t, "wal", strings.ToLower(journalMode),
		"DualPool writer must report journal_mode=wal (canonical Cut 6.2 invariant); got %q", journalMode)

	var busyTimeout int
	require.NoError(t, pool.Writer.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
	assert.Equal(t, 5000, busyTimeout,
		"DualPool writer must apply busy_timeout=5000 (canonical Cut 6.2 invariant)")
}

// TestNewDualPool_ForeignKeysEnabled pins the audit DB-01 invariant: both
// handles of the canonical pool enforce the FKs declared in the schema.
// SQLite defaults foreign_keys to OFF, so a writer without this pragma
// silently accepts orphan children.
func TestNewDualPool_ForeignKeysEnabled(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "db01_fk.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	for name, db := range map[string]*sql.DB{"writer": pool.Writer, "reader": pool.Reader} {
		var foreignKeys int
		require.NoError(t, db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "DualPool %s must enforce foreign_keys=1", name)
	}
}

// TestNewDualPool_ForeignKeyViolationIsRejected proves the pragma is not
// merely reported: an orphan child insert fails at the pool boundary.
func TestNewDualPool_ForeignKeyViolationIsRejected(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "db01_fk_violation.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	_, err = pool.Writer.ExecContext(ctx, `CREATE TABLE parent (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = pool.Writer.ExecContext(ctx, `CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL, FOREIGN KEY (parent_id) REFERENCES parent(id) ON DELETE CASCADE)`)
	require.NoError(t, err)

	_, err = pool.Writer.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES ('c1', 'missing')`)
	require.Error(t, err, "an orphan child insert must fail with foreign_keys=ON")
	assert.Contains(t, strings.ToUpper(err.Error()), "FOREIGN KEY")

	// The same insert succeeds once the parent exists, and the cascade
	// removes the child when the parent is deleted.
	_, err = pool.Writer.ExecContext(ctx, `INSERT INTO parent (id) VALUES ('p1')`)
	require.NoError(t, err)
	_, err = pool.Writer.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES ('c1', 'p1')`)
	require.NoError(t, err)
	_, err = pool.Writer.ExecContext(ctx, `DELETE FROM parent WHERE id = 'p1'`)
	require.NoError(t, err)

	var children int
	require.NoError(t, pool.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM child`).Scan(&children))
	assert.Zero(t, children, "ON DELETE CASCADE must remove the child")
}

// TestNewDualPool_MaxOpenConns_PerPool pins the per-pool sizing
// invariant: writer MaxOpenConns=1, reader MaxOpenConns=N (N>=2).
func TestNewDualPool_MaxOpenConns_PerPool(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_conns.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const numReaders = 3
	pool, err := NewDualPool(ctx, tmpFile, numReaders)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	assert.Equal(t, 1, pool.Writer.Stats().MaxOpenConnections,
		"writer pool MaxOpenConnections MUST stay at 1 (canonical single-writer)")
	assert.Equal(t, numReaders, pool.Reader.Stats().MaxOpenConnections,
		"reader pool MaxOpenConnections MUST equal numReaders argument (canonical concurrent-reader affordance)")
	assert.NotSame(t, pool.Writer, pool.Reader,
		"writer and reader must be independent sql.DB pools")
}

func TestAttachDualPoolIsNotUsedForProductionReaderIsolation(t *testing.T) {
	primary, err := NewSQLiteDB(t.TempDir(), "media.db.sqlite", zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = primary.Close() }()

	pool, err := NewDualPool(context.Background(), primary.Path(), 2)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	assert.NotSame(t, primary.DB, pool.Reader,
		"production dual pool reader must not alias DatabaseSet.Primary")
	assert.NotSame(t, primary.DB, pool.Writer,
		"production dual pool writer must not alias DatabaseSet.Primary")
}

// TestNewDualPool_EmptyURIFailsClosed pins the godlike/07 contract.
func TestNewDualPool_EmptyURIFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, "", 2)
	require.Error(t, err)
	assert.Nil(t, pool)
	assert.Contains(t, err.Error(), "empty fileUri",
		"empty fileUri MUST report 'empty fileUri' (canonical fail-closed message)")
}

// ── walPragmas enrichment ────────────────────────────────────────────────

// TestWalPragmas_PreservesAllThreePragmas: url.Values.Set on the same
// key REPLACES, url.Values.Add APPENDS. Set 3x wastes 2 pragmas; Add
// 3x preserves all of them in the encoded URI.
//
// AUDIT (PR-CANONICAL-WAL-PRAGMAS-ENCODE, July 2026): walPragmas() uses
// the canonical Go URL composer url.Values.Encode() which percent-
// encodes reserved characters — the parens in `_pragma=journal_mode(WAL)`
// surface as `_pragma=journal_mode%28WAL%29` in the encoded URI. The
// driver (mattn/go-sqlite3) correctly decodes %28/%29 to ( ) at runtime
// (proven by sibling test TestNewDualPool_WALModeApplied which queries
// PRAGMA journal_mode directly). So the assertion target is the
// UNESCAPED form (matches the canonical human-readable intent of the
// pragmas); the encoded form is the wire-shape, not the assertion
// contract. ARCH-ALLOWLISTED: issues.yaml::PRE-EXISTING-1 follow_up
// bullet 3 (assertion fix surface; production is canonical godlike/06
// SSOT go URL composer).
func TestWalPragmas_PreservesAllThreePragmas(t *testing.T) {
	uri := "/tmp/cut62_wal_pragmas.db"
	out, err := walPragmas(uri)
	require.NoError(t, err)

	// Unescape the encoded URI so the assertions operate on the
	// canonical pragma form. _busy_timeout=5000 has no reserved
	// chars so unescape is a no-op for that segment — the
	// back-compat check below still passes identically.
	unescapedOut, err := url.QueryUnescape(out)
	require.NoError(t, err, "url.QueryUnescape must accept walPragmas output verbatim (godlike/07 fail-closed contract)")

	assert.Contains(t, unescapedOut, "_pragma=journal_mode(WAL)",
		"WAL pragma MUST survive encoding (canonical concurrent-reader affordance)")
	assert.Contains(t, unescapedOut, "_pragma=busy_timeout(5000)",
		"busy_timeout pragma MUST survive encoding (canonical retry.IsTransient ErrBusy loop)")
	assert.Contains(t, unescapedOut, "_pragma=synchronous(NORMAL)",
		"synchronous pragma MUST survive encoding (WAL-safe durability)")
	assert.Contains(t, unescapedOut, "_busy_timeout=5000",
		"_busy_timeout parameter MUST survive encoding (legacy driver-level pendant)")
	assert.Contains(t, unescapedOut, "_pragma=foreign_keys(1)",
		"foreign_keys pragma MUST survive encoding (audit DB-01 integrity invariant)")

	// Count=1 invariant preserved across the unescape: each pragma
	// was added exactly once via q.Add in walPragmas(); url.Values.Encode
	// rewrites the form but never collapses or duplicates entries.
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=journal_mode(WAL)"),
		"journal_mode pragma MUST appear exactly once")
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=busy_timeout(5000)"),
		"busy_timeout pragma MUST appear exactly once")
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=synchronous(NORMAL)"),
		"synchronous pragma MUST appear exactly once")
}

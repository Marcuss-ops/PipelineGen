// Package assets — source_version_test.go pins the canonical priority
// chain against actual SQLite + JSON1 so the consumer-side contract is
// guarded against silent drift.
//
// Test strategy: each test inserts a synthetic media_assets row with a
// controlled metadata_json + legacy_file_md5 pair, then asserts which tier
// the COALESCE chain picked. sqlmock-style stubs would not honour
// `json_extract()` (SQLite extension) so a real in-memory database is
// required; mattn/go-sqlite3 bundles JSON1 so this works without a
// build-tag fork.
package imagesregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB opens an in-memory SQLite + applies the canonical
// media_assets schema (subset of the production migration sufficient
// for the 5 priority slots). Returns the *sql.DB so callers can insert
// synthetic rows.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { _ = db.Close() })

	// Minimal canonical schema — only the columns SourceVersionFor
	// touches via COALESCE/index_path.legacy_file_md5 is the legacy
	// top-level column (tier 3). metadata_json is the JSON bag (tiers
	// 0 + 1 + 2 via json_extract).
	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`)
	require.NoError(t, err, "create media_assets")
	return db
}

// insertRow writes a synthetic media_assets row with controlled
// metadata_json + file_hash so a test can pin which tier
// SourceVersionFor should pick.
func insertRow(t *testing.T, db *sql.DB, id, metadataJSON, fileHash string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, metadata_json, legacy_file_md5) VALUES (?, ?, ?)`,
		id, metadataJSON, fileHash,
	)
	require.NoError(t, err, "insert synthetic row %s", id)
}

// ── 1. sql.ErrNoRows sentinel ────────────────────────────────────────

// TestSourceVersionFor_AssetNotFoundReturnsErrNoRows: missing assetID
// surfaces sql.ErrNoRows so the producer (cmd/admin) can wrap the
// fail-closed diagnostic AND so the consumer (outbox handler) can
// distinguish "row missing" (fall-through to IndexClip) from "row
// exists but no fingerprint" (also fall-through, but for a different
// reason). errors.Is is required because sql drivers may wrap.
func TestSourceVersionFor_AssetNotFoundReturnsErrNoRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	got, err := SourceVersionFor(context.Background(), db, "ghost-id")
	require.Error(t, err, "missing row must error; got %q", got)
	assert.True(t, errors.Is(err, sql.ErrNoRows),
		"missing-row sentinel must be sql.ErrNoRows (errors.Is match); got %v", err)
	assert.Equal(t, "", got, "missing-row value must be empty string")
}

// ── 2. Tier 0 — metadata_json.$.index_revision wins ──────────────────

// TestSourceVersionFor_Tier0IndexRevisionWins: when index_revision is
// populated inside metadata_json, it MUST be the return value even if
// content_hash (byte identity), file_hash, AND the top-level legacy
// column are populated with different values. Pin: index_revision is
// the indexable-snapshot fingerprint the supersede gate compares, and
// it takes precedence over the byte-identity chain (godlike/06
// content_sha256 vs index_revision separation, Aug 2026).
func TestSourceVersionFor_Tier0IndexRevisionWins(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "asset-0",
		`{"index_revision":"rev-T0","content_hash":"hash-T1","legacy_file_md5":"hash-T2-fallback"}`,
		"hash-T3-legacy",
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-0")
	require.NoError(t, err)
	assert.Equal(t, "rev-T0", got,
		"tier 0 index_revision MUST win on disagreement (content_sha256 vs index_revision separation)")
}

// ── 3. Tier 1 — metadata_json.$.content_hash wins ────────────────────

// TestSourceVersionFor_Tier1ContentHashWins: when content_hash is
// populated inside metadata_json (and index_revision is absent), it
// MUST be the return value even if metadata_json.$.file_hash AND the
// top-level legacy_file_md5 column are populated with different values.
// Pin: content_hash wins on disagreement (collapse rule from PR 11).
func TestSourceVersionFor_Tier1ContentHashWins(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "asset-1",
		`{"content_hash":"hash-T1","legacy_file_md5":"hash-T2-fallback"}`,
		"hash-T3-legacy",
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-1")
	require.NoError(t, err)
	assert.Equal(t, "hash-T1", got,
		"tier 1 content_hash MUST win on disagreement (PR 11 lock-step rule)")
}

// ── 4. Tier 2 — metadata_json.$.file_hash fills the gap ──────────────

// TestSourceVersionFor_Tier2FileHashWins: when content_hash is absent
// but metadata_json.$.file_hash is populated, tier 2 wins. The
// top-level legacy legacy_file_md5 column is ignored (tier 3 only fills in
// when tier 2 is also absent).
func TestSourceVersionFor_Tier2FileHashWins(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "asset-2",
		`{"file_hash":"hash-T2"}`, /* no content_hash */
		"hash-T3-legacy",          /* top-level present but ignored */
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-2")
	require.NoError(t, err)
	assert.Equal(t, "hash-T2", got,
		"tier 2 JSON file_hash MUST win when tier 1 absent")
}

// ── 5. Tier 3 — top-level file_hash legacy column ────────────────────

// TestSourceVersionFor_Tier3LegacyColumnWins: when both JSON tiers
// are absent, the legacy top-level media_assets.legacy_file_md5 column is
// the canonical fingerprint. This is the slot that DRIFTED in the
// pre-PR-11-followup consumer (which used Asset.LegacyFileMD5() reading
// the same metadata slot as tier 2 — effectively skipping tier 3).
// The helper now reads the actual top-level column so backfilled
// rows WITHOUT metadata_json._hash but WITH file_hash are correctly
// fingerprinted.
func TestSourceVersionFor_Tier3LegacyColumnWins(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "asset-3",
		`{}`, /* no content_hash, no file_hash in JSON */
		"hash-T3-legacy",
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-3")
	require.NoError(t, err)
	assert.Equal(t, "hash-T3-legacy", got,
		"tier 3 legacy top-level file_hash MUST fill when JSON tiers absent")
}

// ── 6. All tiers empty ───────────────────────────────────────────────

// TestSourceVersionFor_AllTiersEmpty: when ALL three slots are
// empty, the COALESCE falls through to the empty-string literal,
// returning ("", nil). callers treat empty as "no fingerprint" —
// the producer is fail-closed (cannot derive event_key), the
// consumer falls through to IndexClip's own internal check.
func TestSourceVersionFor_AllTiersEmpty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "asset-empty",
		`{}`,
		"",
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-empty")
	require.NoError(t, err)
	assert.Equal(t, "", got,
		"all tiers empty MUST return empty string with nil error (producer fail-closed; consumer fall-through)")
}

// ── 7. Caller-error guards ───────────────────────────────────────────

// TestSourceVersionFor_NilQuerierErrors: passing a nil querier
// surfaces an explicit caller-error rather than a runtime nil
// dereference panic. The caller contract is documented in the
// helper doc-comment.
func TestSourceVersionFor_NilQuerierErrors(t *testing.T) {
	t.Parallel()
	got, err := SourceVersionFor(context.Background(), nil, "any-id")
	require.Error(t, err)
	assert.False(t, errors.Is(err, sql.ErrNoRows),
		"nil-querier guard MUST NOT be sql.ErrNoRows (caller-misuse path)")
	assert.Equal(t, "", got)
	assert.Contains(t, err.Error(), "querier must not be nil",
		"diagnostic must name the misuse")
}

// TestSourceVersionFor_EmptyAssetIDErrors: passing an empty assetID
// surfaces an explicit caller-error rather than triggering a SQLite
// "syntax error" or scanning-all-rows path. Same rationale as the
// nil-querier guard.
func TestSourceVersionFor_EmptyAssetIDErrors(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	got, err := SourceVersionFor(context.Background(), db, "")
	require.Error(t, err)
	assert.False(t, errors.Is(err, sql.ErrNoRows),
		"empty-assetID guard MUST NOT be sql.ErrNoRows")
	assert.Equal(t, "", got)
	assert.Contains(t, err.Error(), "assetID must not be empty",
		"diagnostic must name the misuse")
}

// ── 8. *sql.Tx acceptance (structural-interface contract) ────────────

// TestSourceVersionFor_AcceptsSQLTx: the contract is that *sql.Tx
// (used by cmd/admin producer) AND *sql.DB (used by outbox consumer)
// both satisfy QueryRowContexter. The DB foundry is exercised in
// the other tests; here we add a *sql.Tx foundry so the acceptance
// test pins the producer-side path too.
//
// Why this matters: PostgreSQL "sql.Scanner" trap is not a real Go
// concern but a future driver wrapper that only implements
// QueryRowContext on *sql.DB would silently fail the producer side
// at runtime. Pinning with a tx here catches that drift at compile
// time of the test (the *sql.Tx must satisfy QueryRowContexter or
// the test fails to type-check).
func TestSourceVersionFor_AcceptsSQLTx(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	insertRow(t, db, "tx-asset",
		`{"content_hash":"tx-hash"}`,
		"tx-legacy",
	)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	got, ferr := SourceVersionFor(context.Background(), tx, "tx-asset")
	require.NoError(t, ferr)
	assert.Equal(t, "tx-hash", got,
		"*sql.Tx MUST be accepted by QueryRowContexter — producer-side path")
}

// ── 9. JSON empty-string corner ──────────────────────────────────────

// TestSourceVersionFor_JsonEmptyStringShortCircuits: COALESCE
// treats JSON's empty-string value (`{"content_hash":""}`) as
// non-NULL — so the chain short-circuits on tier 1 ("") regardless
// of tier 2 / tier 3 contents. This is the JSON-extract vs JSON-
// absent corner; the producer's fail-closed empty-contentHash
// downstream check rescues it on the producer side, but pinning
// the helper behaviour explicitly prevents future "tier 1 wins on
// empty string" regressions that would silently lose tier 2/3
// data.
//
// Contrast with TestSourceVersionFor_AllTiersEmpty above, which
// uses `{}` (json_extract returns SQL NULL, full chain fall-through
// to the literal `”`). The two corners are distinct bugs that
// mirror the user-data shape; both matter.
func TestSourceVersionFor_JsonEmptyStringShortCircuits(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// json_extract("$.content_hash") returns "" (empty string,
	// non-null) since the key is present-but-empty. COALESCE
	// picks the empty string and short-circuits. Tier 2 has
	// "good-tier-2-hash" but it's never reached.
	insertRow(t, db, "asset-empty-json",
		`{"content_hash":"","legacy_file_md5":"good-tier-2-hash"}`,
		"good-tier-3-legacy",
	)
	got, err := SourceVersionFor(context.Background(), db, "asset-empty-json")
	require.NoError(t, err)
	assert.Equal(t, "", got,
		"JSON empty-string value MUST short-circuit the COALESCE chain "+
			"(tier 1 wins even on empty); producer's empty-fail-closed check rescues")
}

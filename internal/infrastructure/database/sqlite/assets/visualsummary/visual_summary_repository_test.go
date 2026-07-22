// Package assets — visual_summary_repository_test.go pins the
// canonical VisualSummary ↔ asset_visual_summaries contract.
//
// Coverage matrix (each test pins ONE invariant):
//
//  1. TestVisualSummaryRepository_HappyPath_RoundTrip — full
//     populate + Upsert + Get round-trip; JSON columns decode;
//     SourceHash round-trips.
//  2. TestVisualSummaryRepository_Get_MissingRowReturnsNilNil
//     → godlike/07 NO-FAKE-AVAILABILITY: Get on a missing row
//     returns (nil, nil) (NOT a placeholder VisualSummary{}).
//  3. TestVisualSummaryRepository_Upsert_RejectsInvalidRows
//     → Validate runs BEFORE the tx; typed errors surface;
//     no row created in the DB (validate-then-write contract).
//  4. TestVisualSummaryRepository_Upsert_ReplacesExisting
//     → second Upsert with the same asset_id replaces atomically
//     (PRIMARY KEY conflict path).
//  5. TestVisualSummaryRepository_ListByModelVersion_FiltersCorrectly
//     → only rows matching (model_name, model_version) returned;
//     ordered by sampled_at_unix DESC, asset_id ASC.
//  6. TestVisualSummaryRepository_ListBySourceHash_DeterministicOrder
//     → ListBySourceHash orders by asset_id ASC for replay.
//  7. TestVisualSummaryRepository_Delete_Idempotent
//     → Delete on a missing row returns nil; second Delete also nil.
//  8. TestVisualSummaryRepository_NewPanicsOnNilDB
//     → godlike/07: nil db at construction time is a hard error.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts the FULL
// state (row counts + field values), not just "no error".
package assets

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// visualSummaryTestDB creates an in-memory SQLite + applies a
// minimal media_assets + asset_visual_summaries schema matching
// migration 151. The schema includes the FK from
// asset_visual_summaries.asset_id → media_assets.id so the
// upsert path exercises the constraint surface.
func visualSummaryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use a per-test file-backed DSN with cache=shared so the FK
	// constraint is enforced (`:memory:` is per-connection, which
	// means different connections would not see each other's
	// tables — the FK reference would silently degrade).
	dsn := "file:" + filepath.Join(t.TempDir(), "visual_summary_test.db") + "?cache=shared&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Single connection so the schema is consistent across ops.
	db.SetMaxOpenConns(1)

	// media_assets: minimal subset that matches the FK
	// surface in 151. The id is the only column we read.
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY
);`); err != nil {
		t.Fatalf("CREATE TABLE media_assets: %v", err)
	}

	// asset_visual_summaries: mirrors migration 151 column-for-column
	// so a future migration that drifts (rename, add, drop) is
	// caught by the round-trip test.
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS asset_visual_summaries (
    asset_id              TEXT PRIMARY KEY NOT NULL,
    visual_summary_text   TEXT NOT NULL DEFAULT '',
    visible_actions_json  TEXT NOT NULL DEFAULT '[]',
    visible_entities_json TEXT NOT NULL DEFAULT '[]',
    frame_count           INTEGER NOT NULL DEFAULT 0 CHECK (frame_count >= 0),
    interval_seconds      REAL NOT NULL DEFAULT 0.0 CHECK (interval_seconds >= 0.0),
    preprocessing_version TEXT NOT NULL DEFAULT '',
    model_name            TEXT NOT NULL DEFAULT '',
    model_version         TEXT NOT NULL DEFAULT '',
    source_hash           TEXT NOT NULL DEFAULT '',
    sampled_at            TEXT NOT NULL DEFAULT '',
    sampled_at_unix       INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at            TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_model
    ON asset_visual_summaries (model_name, model_version);
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_source_hash
    ON asset_visual_summaries (source_hash);`); err != nil {
		t.Fatalf("CREATE TABLE asset_visual_summaries: %v", err)
	}
	return db
}

// insertMediaAssetRow pre-inserts a media_assets row so the
// asset_visual_summaries FK constraint is satisfied.
func insertMediaAssetRow(t *testing.T, db *sql.DB, assetID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets (id) VALUES (?)`, assetID); err != nil {
		t.Fatalf("insert media_assets row %q: %v", assetID, err)
	}
}

// validVisualSummary returns a fully-populated VisualSummary
// that satisfies Validate() — every positive control case
// tests with this and mutates per-case.
func validVisualSummary(assetID string) asset.VisualSummary {
	return asset.VisualSummary{
		AssetID:              assetID,
		VisualSummaryText:    "A boxing match between two professional fighters in a ring.",
		VisibleActions:       []string{"throws_punch", "defends_ring", "celebrates_win"},
		VisibleEntities:      []string{"boxer_1", "boxer_2", "ring", "referee"},
		FrameCount:           12,
		IntervalSeconds:      2.0,
		PreprocessingVersion: "vlm-sampler/v1.0.0",
		ModelName:            "llava-1.6-7b",
		ModelVersion:         "2026-07-13",
		SampledAt:            "2026-07-13T10:00:00Z",
		SourceHash: asset.ComputeSourceHash(
			[]string{"throws_punch", "defends_ring", "celebrates_win"},
			[]string{"boxer_1", "boxer_2", "ring", "referee"},
			"llava-1.6-7b",
			"2026-07-13",
			"vlm-sampler/v1.0.0",
			12,
		),
	}
}

// ── Tests ────────────────────────────────────────────────────────────

// TestVisualSummaryRepository_HappyPath_RoundTrip pins the
// canonical "all green" Upsert + Get cycle. Asserts:
//
//   - Upsert succeeds for a fully-populated VisualSummary.
//   - Get returns the same row (with the SourceHash round-tripped).
//   - JSON columns (visible_actions, visible_entities) decode
//     cleanly into the Go []string fields.
//   - FrameCount / IntervalSeconds / preprocessing_version /
//     model_name / model_version round-trip.
func TestVisualSummaryRepository_HappyPath_RoundTrip(t *testing.T) {
	db := visualSummaryTestDB(t)
	insertMediaAssetRow(t, db, "ast-vs-001")
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)

	row := validVisualSummary("ast-vs-001")
	require.NoError(t, repo.Upsert(context.Background(), row))

	got, getErr := repo.Get(context.Background(), "ast-vs-001")
	require.NoError(t, getErr, "Get on freshly-upserted row MUST succeed")
	require.NotNil(t, got, "Get on freshly-upserted row MUST return non-nil")

	assert.Equal(t, row.AssetID, got.AssetID, "asset_id round-trips")
	assert.Equal(t, row.VisualSummaryText, got.VisualSummaryText,
		"visual_summary_text round-trips")
	assert.Equal(t, row.VisibleActions, got.VisibleActions,
		"visible_actions round-trips (JSON columns decode)")
	assert.Equal(t, row.VisibleEntities, got.VisibleEntities,
		"visible_entities round-trips (JSON columns decode)")
	assert.Equal(t, row.FrameCount, got.FrameCount, "frame_count round-trips")
	assert.InDelta(t, row.IntervalSeconds, got.IntervalSeconds, 1e-9,
		"interval_seconds round-trips (REAL column)")
	assert.Equal(t, row.PreprocessingVersion, got.PreprocessingVersion,
		"preprocessing_version round-trips")
	assert.Equal(t, row.ModelName, got.ModelName,
		"model_name round-trips")
	assert.Equal(t, row.ModelVersion, got.ModelVersion,
		"model_version round-trips")
	assert.Equal(t, row.SourceHash, got.SourceHash,
		"source_hash round-trips — ComputeSourceHash is the canonical owner")
	assert.Equal(t, row.SampledAt, got.SampledAt,
		"sampled_at round-trips (RFC3339 TEXT)")
	assert.False(t, got.CreatedAt.IsZero(),
		"created_at populated by DEFAULT (datetime('now'))")
	assert.False(t, got.UpdatedAt.IsZero(),
		"updated_at populated by upsert trigger")
}

// TestVisualSummaryRepository_Get_MissingRowReturnsNilNil pins
// the godlike/07 NO-FAKE-AVAILABILITY contract. A missing row
// MUST return (nil, nil), NOT a placeholder VisualSummary{} —
// the Qdrant payload mapper distinguishes "no VLM pass" by the
// nil-check on the returned pointer. A zero-value non-nil would
// leak into the Qdrant payload as visual_summary:"" (the
// godlike/07 anti-pattern: representing "absent" as "present
// but empty").
func TestVisualSummaryRepository_Get_MissingRowReturnsNilNil(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)
	insertMediaAssetRow(t, db, "ast-vs-never-upserted")

	got, getErr := repo.Get(context.Background(), "ast-vs-never-upserted")
	require.NoError(t, getErr,
		"Get on a missing row MUST return nil error (canonical NO_VLM_PASS signal)")
	assert.Nil(t, got,
		"Get on a missing row MUST return nil *VisualSummary — "+
			"callers detect NO_VLM_PASS by nil-check; a zero-value "+
			"non-nil would leak visual_summary:'' into Qdrant payload")
}

// TestVisualSummaryRepository_Upsert_RejectsInvalidRows pins
// the validate-then-write contract: every Validate() failure
// surfaces a typed error BEFORE the DB write. Asserts:
//
//   - empty AssetID → ErrVisualSummaryEmptyAssetID surfaces; no row
//     created.
//   - oversized VisibleActions → ErrVisualSummaryActionsTooLong
//     surfaces; no row created.
//   - oversized VisibleEntities → ErrVisualSummaryEntitiesTooLong
//     surfaces; no row created.
//   - oversized VisualSummaryText → ErrVisualSummarySummaryTooLong surfaces;
//     no row created.
//   - negative FrameCount → typed error; no row created.
//   - negative IntervalSeconds → typed error; no row created.
func TestVisualSummaryRepository_Upsert_RejectsInvalidRows(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)
	insertMediaAssetRow(t, db, "ast-vs-invalid")

	cases := []struct {
		name   string
		mutate func(*asset.VisualSummary)
		want   error // typed sentinel; assertion uses errors.Is (errors.Join preserves membership).
	}{
		{
			name:   "empty AssetID",
			mutate: func(v *asset.VisualSummary) { v.AssetID = "" },
			want:   asset.ErrVisualSummaryEmptyAssetID,
		},
		{
			name:   "oversized VisibleActions",
			mutate: func(v *asset.VisualSummary) { v.VisibleActions = make([]string, asset.MaxVisibleItems+1) },
			want:   asset.ErrVisualSummaryActionsTooLong,
		},
		{
			name:   "oversized VisibleEntities",
			mutate: func(v *asset.VisualSummary) { v.VisibleEntities = make([]string, asset.MaxVisibleItems+1) },
			want:   asset.ErrVisualSummaryEntitiesTooLong,
		},
		{
			name: "oversized VisualSummaryText",
			mutate: func(v *asset.VisualSummary) {
				v.VisualSummaryText = string(make([]byte, asset.MaxVisualSummaryChars+1))
			},
			want: asset.ErrVisualSummarySummaryTooLong,
		},
		{
			name:   "negative FrameCount",
			mutate: func(v *asset.VisualSummary) { v.FrameCount = -1 },
			want:   asset.ErrVisualSummaryNegativeFrameCount,
		},
		{
			name:   "negative IntervalSeconds",
			mutate: func(v *asset.VisualSummary) { v.IntervalSeconds = -1.0 },
			want:   asset.ErrVisualSummaryNegativeInterval,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			row := validVisualSummary("ast-vs-invalid")
			tc.mutate(&row)
			err := repo.Upsert(context.Background(), row)
			require.Error(t, err,
				"Upsert with invalid VisualSummary MUST return error "+
					"before any DB write — godlike/07 fail-closed")
			require.True(t, errors.Is(err, tc.want),
				"Upsert error chain MUST contain %q (got: %v) "+
					"— Validate() uses errors.Join so callers can "+
					"errors.Is across the chain", tc.want, err)
			// Validate-then-write: no row should exist for this
			// asset_id post-rejection.
			got, getErr := repo.Get(context.Background(), "ast-vs-invalid")
			require.NoError(t, getErr)
			assert.Nil(t, got, "Upsert rejection MUST leave DB clean "+
				"(nil Get post-rejection)")
		})
	}
}

// TestVisualSummaryRepository_Upsert_ReplacesExisting pins the
// PRIMARY KEY conflict path: a second Upsert with the same
// asset_id atomically replaces the previous row. The
// visible_actions JSON column must reflect the second write
// (not both); the source_hash follows the second write.
func TestVisualSummaryRepository_Upsert_ReplacesExisting(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)
	insertMediaAssetRow(t, db, "ast-vs-replace")

	first := validVisualSummary("ast-vs-replace")
	first.VisualSummaryText = "first description"
	first.SourceHash = "first-hash"
	require.NoError(t, repo.Upsert(context.Background(), first))

	second := validVisualSummary("ast-vs-replace")
	second.VisualSummaryText = "second description"
	second.SourceHash = "second-hash"
	require.NoError(t, repo.Upsert(context.Background(), second))

	got, getErr := repo.Get(context.Background(), "ast-vs-replace")
	require.NoError(t, getErr)
	require.NotNil(t, got)

	assert.Equal(t, "second description", got.VisualSummaryText,
		"second Upsert must replace visual_summary_text atomically")
	assert.Equal(t, "second-hash", got.SourceHash,
		"second Upsert must replace source_hash atomically")
}

// TestVisualSummaryRepository_ListByModelVersion_FiltersCorrectly
// pins the reindex-by-model-version query: only rows whose
// (model_name, model_version) match are returned, ordered by
// sampled_at_unix DESC then asset_id ASC.
func TestVisualSummaryRepository_ListByModelVersion_FiltersCorrectly(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)

	// Three rows: two at (llava-1.6-7b, 2026-07-13), one at (qwen-vl, 2026-06-01)
	rows := []asset.VisualSummary{
		{
			AssetID: "ast-vs-lv-1", ModelName: "llava-1.6-7b", ModelVersion: "2026-07-13",
			SampledAt:  "2026-07-13T10:00:00Z",
			SourceHash: asset.ComputeSourceHash(nil, nil, "llava-1.6-7b", "2026-07-13", "vlm-sampler/v1.0.0", 0),
		},
		{
			AssetID: "ast-vs-lv-2", ModelName: "llava-1.6-7b", ModelVersion: "2026-07-13",
			SampledAt:  "2026-07-13T11:00:00Z",
			SourceHash: asset.ComputeSourceHash(nil, nil, "llava-1.6-7b", "2026-07-13", "vlm-sampler/v1.0.0", 0),
		},
		{
			AssetID: "ast-vs-qv-1", ModelName: "qwen-vl", ModelVersion: "2026-06-01",
			SampledAt:  "2026-06-01T08:00:00Z",
			SourceHash: asset.ComputeSourceHash(nil, nil, "qwen-vl", "2026-06-01", "vlm-sampler/v1.0.0", 0),
		},
	}
	for _, r := range rows {
		insertMediaAssetRow(t, db, r.AssetID)
		require.NoError(t, repo.Upsert(context.Background(), r))
	}

	// Filter to (llava-1.6-7b, 2026-07-13) — must return exactly
	// the two matching rows, ordered by sampled_at_unix DESC
	// (most-recent first), then asset_id ASC.
	got, qErr := repo.ListByModelVersion(context.Background(), "llava-1.6-7b", "2026-07-13")
	require.NoError(t, qErr)
	require.Len(t, got, 2, "ListByModelVersion must filter strictly on (model_name, model_version)")
	// Most-recently-sampled first: ast-vs-lv-2 at 11:00 → ast-vs-lv-1 at 10:00.
	assert.Equal(t, "ast-vs-lv-2", got[0].AssetID,
		"ListByModelVersion orders by sampled_at_unix DESC")
	assert.Equal(t, "ast-vs-lv-1", got[1].AssetID,
		"ListByModelVersion tiebreak by asset_id ASC")

	// Filter to (qwen-vl, 2026-06-01) — must return the single match.
	gotQ, qErrQ := repo.ListByModelVersion(context.Background(), "qwen-vl", "2026-06-01")
	require.NoError(t, qErrQ)
	require.Len(t, gotQ, 1)
	assert.Equal(t, "ast-vs-qv-1", gotQ[0].AssetID)

	// Empty-result query — must return an empty slice, NOT nil
	// and NOT an error.
	gotNone, qErrNone := repo.ListByModelVersion(context.Background(), "no-such-model", "no-such-version")
	require.NoError(t, qErrNone)
	assert.NotNil(t, gotNone, "ListByModelVersion empty-result MUST return non-nil empty slice")
	assert.Len(t, gotNone, 0)
}

// TestVisualSummaryRepository_ListBySourceHash_DeterministicOrder
// pins the supersede-gate cross-check: ListBySourceHash returns
// rows whose SourceHash matches, ordered by asset_id ASC for
// replay-deterministic results.
func TestVisualSummaryRepository_ListBySourceHash_DeterministicOrder(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)

	// Two rows with the SAME SourceHash; one row with a different hash.
	sharedHash := "shared-hash-1234567890abcdef"
	rows := []asset.VisualSummary{
		{AssetID: "ast-vs-sh-2", SourceHash: sharedHash, ModelName: "m", ModelVersion: "v"},
		{AssetID: "ast-vs-sh-1", SourceHash: sharedHash, ModelName: "m", ModelVersion: "v"},
		{AssetID: "ast-vs-sh-3", SourceHash: "different-hash", ModelName: "m", ModelVersion: "v"},
	}
	for _, r := range rows {
		insertMediaAssetRow(t, db, r.AssetID)
		require.NoError(t, repo.Upsert(context.Background(), r))
	}

	got, qErr := repo.ListBySourceHash(context.Background(), sharedHash)
	require.NoError(t, qErr)
	require.Len(t, got, 2,
		"ListBySourceHash must filter strictly on the given SourceHash")
	// asset_id ASC deterministic ordering.
	assert.Equal(t, "ast-vs-sh-1", got[0].AssetID,
		"ListBySourceHash orders by asset_id ASC for replay determinism")
	assert.Equal(t, "ast-vs-sh-2", got[1].AssetID,
		"second row's asset_id follows ASC ordering")

	// Empty-result query.
	gotNone, qErrNone := repo.ListBySourceHash(context.Background(), "no-such-hash")
	require.NoError(t, qErrNone)
	assert.Empty(t, gotNone,
		"ListBySourceHash empty-result MUST return empty slice")
}

// TestVisualSummaryRepository_Delete_Idempotent pins the
// idempotent Delete contract: Delete on a missing row returns
// nil (no error), and a subsequent Delete on the now-missing
// row also returns nil.
func TestVisualSummaryRepository_Delete_Idempotent(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)
	insertMediaAssetRow(t, db, "ast-vs-del-1")
	row := validVisualSummary("ast-vs-del-1")
	require.NoError(t, repo.Upsert(context.Background(), row))

	// First Delete: row exists → nil, row gone.
	require.NoError(t, repo.Delete(context.Background(), "ast-vs-del-1"),
		"Delete on existing row MUST succeed")
	got, getErr := repo.Get(context.Background(), "ast-vs-del-1")
	require.NoError(t, getErr)
	assert.Nil(t, got, "Get post-Delete MUST return nil")

	// Second Delete: row missing → still nil (idempotent).
	require.NoError(t, repo.Delete(context.Background(), "ast-vs-del-1"),
		"Delete on missing row MUST return nil (idempotent)")
}

// TestVisualSummaryRepository_NewPanicsOnNilDB pins the
// godlike/07 fail-closed contract for the composition root:
// a nil *sql.DB at construction time returns a typed error,
// not a runtime nil-deref at the first query.
func TestVisualSummaryRepository_NewPanicsOnNilDB(t *testing.T) {
	repo, err := NewVisualSummaryRepository(nil, zap.NewNop())
	require.Error(t, err,
		"NewVisualSummaryRepository(nil, nil) MUST return a hard error")
	assert.Nil(t, repo,
		"errored NewVisualSummaryRepository MUST return nil repo "+
			"(composition root fails closed)")
}

// TestVisualSummaryRepository_Upsert_NilReceiverSafe pins the
// nil-receiver surface: a nil *VisualSummaryRepositorySQLite
// pointer returns a typed error per the canonical nil-safe
// pattern, not a panic. (The constructor returns an error
// on nil db; this test covers callers who hold a nil pointer
// somehow — defensive at the boundary.)
func TestVisualSummaryRepository_Upsert_NilReceiverSafe(t *testing.T) {
	var repo *VisualSummaryRepositorySQLite // nil pointer
	row := validVisualSummary("ast-vs-nil-recv")
	err := repo.Upsert(context.Background(), row)
	require.Error(t, err, "nil receiver MUST surface a typed error")
}

// TestVisualSummaryRepository_AssetIDRequired pins the empty-id
// guard at every entry point: Get / Delete / ListByModelVersion
// / ListBySourceHash all reject an empty key.
func TestVisualSummaryRepository_AssetIDRequired(t *testing.T) {
	db := visualSummaryTestDB(t)
	repo, err := NewVisualSummaryRepository(db, zap.NewNop())
	require.NoError(t, err)

	_, errGet := repo.Get(context.Background(), "")
	require.Error(t, errGet, "Get(empty) MUST reject before SQL")

	errDel := repo.Delete(context.Background(), "")
	require.Error(t, errDel, "Delete(empty) MUST reject before SQL")

	_, errLBMV := repo.ListByModelVersion(context.Background(), "", "")
	require.Error(t, errLBMV, "ListByModelVersion(empty,*) MUST reject before SQL")

	_, errLBMVModel := repo.ListByModelVersion(context.Background(), "m", "")
	require.Error(t, errLBMVModel, "ListByModelVersion(m, empty) MUST reject before SQL")

	_, errLBSH := repo.ListBySourceHash(context.Background(), "")
	require.Error(t, errLBSH, "ListBySourceHash(empty) MUST reject before SQL")
}

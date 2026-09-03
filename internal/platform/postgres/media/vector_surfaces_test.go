// Package media — vector_surfaces_test.go: DSN-gated live tests for the
// pgvector derived surfaces (media_asset_features, media_embeddings,
// media_embedding_families) and the pgvector MediaSearcher.
//
// Implements the POSTGRES-MEDIA-CUTOVER acceptance criteria:
//
//   - one PostgreSQL transaction commits asset+location+features+embedding
//   - rollback leaves zero partial state
//   - filtered vector search returns correct asset
//   - duplicate commit (upsert) is idempotent
//   - embedding model version (family identity) is preserved
//   - unregistered family / dimension mismatch is rejected fail-closed
//   - workspace isolation + lifecycle allow-list enforced in-query
package media_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// countTableColumn counts rows by the asset identity column, mapping
// engine-specific identity columns: media_assets PK is id, outbox_events
// references the asset via aggregate_id. Delegates to the package-level
// countWhere helper (parity_test.go).
func countTableColumn(t *testing.T, db *sql.DB, table, assetIDCol, val string) int {
	t.Helper()
	col := assetIDCol
	switch table {
	case "media_assets":
		col = "id"
	case "outbox_events":
		col = "aggregate_id"
	}
	return countWhere(t, db, table, col, val)
}

func newVectorWriter(t *testing.T) (*pgmedia.VectorSurfaceWriter, *sql.DB) {
	t.Helper()
	db := newMediaTestDB(t)
	return pgmedia.NewVectorSurfaceWriter(db), db
}

// seedAssetWithEmbedding registers the visual family, commits one asset
// through the canonical committer, writes one feature row + one embedding
// — all inside ONE transaction — and returns the resolved model id.
func seedAssetWithEmbedding(t *testing.T, db *sql.DB, assetID string, vec []float32) (string, *pgmedia.VectorSurfaceWriter) {
	t.Helper()
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)

	// One registered production family for the visual channel.
	modelID := "test-siglip-v1"
	if err := w.RegisterEmbeddingFamily(ctx, "visual", modelID, len(vec)); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}

	// Canonical asset commit (steps 1-8) — the same producer path.
	c, _ := newPostgresCommitter(t)
	req := fullCommitRequest()
	req.Asset.AssetID = assetID
	if _, err := c.CommitMediaAsset(ctx, req); err != nil {
		t.Fatalf("CommitMediaAsset: %v", err)
	}

	// Derived surfaces in the SAME transaction: features + embedding.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	features := pgmedia.AssetFeatureRecord{AssetID: assetID, HasFaces: true}
	if err := w.UpsertAssetFeaturesTx(ctx, tx, features); err != nil {
		t.Fatalf("UpsertAssetFeaturesTx: %v", err)
	}
	if err := w.UpsertEmbeddingTx(ctx, tx, assetID, "visual", modelID, vec); err != nil {
		t.Fatalf("UpsertEmbeddingTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	return modelID, w
}

// TestCutover_SingleTransactionCommitsAssetLocationFeaturesEmbedding is
// the core POSTGRES-MEDIA-CUTOVER criterion: one transaction lands the
// asset, its location, its features, and its embedding atomically.
func TestCutover_SingleTransactionCommitsAssetLocationFeaturesEmbedding(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	modelID := "test-siglip-v1"
	if err := w.RegisterEmbeddingFamily(ctx, "visual", modelID, 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}

	// ONE explicit transaction: commit the canonical asset request, then
	// the derived surfaces. Everything lands or nothing does.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := c.CommitTx(ctx, tx, txCommitRequestFor("yt_tx_all_v1")); err != nil {
		t.Fatalf("CommitTx: %v", err)
	}
	if err := w.UpsertAssetFeaturesTx(ctx, tx, pgmedia.AssetFeatureRecord{AssetID: "yt_tx_all_v1", HasFaces: true}); err != nil {
		t.Fatalf("features: %v", err)
	}
	if err := w.UpsertEmbeddingTx(ctx, tx, "yt_tx_all_v1", "visual", modelID, []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, check := range []struct{ table, name string }{
		{"media_assets", "asset"},
		{"asset_locations", "location"},
		{"media_asset_features", "features"},
		{"media_embeddings", "embedding"},
	} {
		if n := countTableColumn(t, db, check.table, "asset_id", "yt_tx_all_v1"); n != 1 {
			t.Fatalf("%s rows = %d, want 1 (asset %s)", check.table, n, check.name)
		}
	}
}

// TestCutover_RollbackLeavesZeroPartialState proves the rollback side of
// the same contract: a failure after the asset upsert leaves ZERO rows on
// every surface.
func TestCutover_RollbackLeavesZeroPartialState(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	if err := w.RegisterEmbeddingFamily(ctx, "visual", "test-siglip-v1", 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := c.CommitTx(ctx, tx, txCommitRequestFor("yt_rollback_v1")); err != nil {
		t.Fatalf("CommitTx: %v", err)
	}
	if err := w.UpsertAssetFeaturesTx(ctx, tx, pgmedia.AssetFeatureRecord{AssetID: "yt_rollback_v1"}); err != nil {
		t.Fatalf("features: %v", err)
	}
	// Unregistered family → the DB trigger fails the embedding upsert.
	err = w.UpsertEmbeddingTx(ctx, tx, "yt_rollback_v1", "visual", "unregistered-model", []float32{0.1, 0.2, 0.3, 0.4})
	if err == nil {
		t.Fatal("expected fail-closed error for unregistered embedding family")
	}
	_ = tx.Rollback()

	for _, table := range []string{"media_assets", "asset_locations", "media_asset_features", "media_embeddings", "outbox_events"} {
		if n := countTableColumn(t, db, table, "asset_id", "yt_rollback_v1"); n != 0 {
			t.Fatalf("rollback left %d row(s) in %s — partial state violated", n, table)
		}
	}
}

// TestCutover_FilteredVectorSearchReturnsCorrectAsset pins filtered ANN:
// only the asset matching hard filters (category, media_type, lifecycle,
// workspace) is returned, ranked by cosine similarity.
func TestCutover_FilteredVectorSearchReturnsCorrectAsset(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	modelID := "test-e5-text-v1"
	if err := w.RegisterEmbeddingFamily(ctx, "text", modelID, 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}

	// Two assets in different categories. Query vector is closest to the
	// "celebrity" asset — the category filter must exclude the other one
	// even though its raw similarity ranks first.
	if _, err := c.CommitMediaAsset(ctx, withTaxonomy(fullCommitRequest(), "celebrity")); err != nil {
		t.Fatalf("commit celebrity: %v", err)
	}
	req2 := withTaxonomy(fullCommitRequest(), "nature")
	req2.Asset.AssetID = "yt_nature_v1"
	if _, err := c.CommitMediaAsset(ctx, req2); err != nil {
		t.Fatalf("commit nature: %v", err)
	}

	// The canonical commit does not set workspace_id; both assets share
	// one workspace so the isolation clause passes and only the category
	// filter differentiates the results.
	if _, err := db.Exec(`UPDATE media_assets SET workspace_id = 'ws-search-1'`); err != nil {
		t.Fatalf("set workspace: %v", err)
	}

	if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "text", modelID, []float32{0.9, 0.0, 0.0, 0.0}); err != nil {
		t.Fatalf("embedding celebrity: %v", err)
	}
	if err := w.UpsertEmbedding(ctx, "yt_nature_v1", "text", modelID, []float32{0.99, 0.0, 0.0, 0.0}); err != nil {
		t.Fatalf("embedding nature: %v", err)
	}

	searcher := pgmedia.NewMediaSearcher(db)
	results, err := searcher.Search(ctx, searchRequestForWorkspace("ws-search-1", "celebrity", []float32{1.0, 0, 0, 0}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1; results=%v", len(results), results)
	}
	if results[0].AssetID != "yt_abc123_10_60_v1" {
		t.Fatalf("asset = %q, want yt_abc123_10_60_v1", results[0].AssetID)
	}
	if results[0].Score <= 0 || results[0].Score > 1.0001 {
		t.Fatalf("cosine similarity = %v, want in (0,1]", results[0].Score)
	}
	// Hydration comes from the same SSOT row.
	if results[0].Name != "Funny Moment" {
		t.Fatalf("hydrated name = %q, want 'Funny Moment'", results[0].Name)
	}
}

// TestCutover_WorkspaceIsolationFailClosed pins the scope invariants:
// empty workspace + non-system fails; the reserved sentinel fails; a
// second workspace never sees the first workspace's rows.
func TestCutover_WorkspaceIsolationFailClosed(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	modelID := "test-e5-text-v1"
	if err := w.RegisterEmbeddingFamily(ctx, "text", modelID, 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}
	if _, err := c.CommitMediaAsset(ctx, withTaxonomy(fullCommitRequest(), "celebrity")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// The canonical commit does not set workspace_id; assign one directly.
	// (outbox_events uses aggregate_id, media_assets uses id — handled by
	// countTableColumn.)
	if _, err := db.Exec(`UPDATE media_assets SET workspace_id = 'ws-a' WHERE id = 'yt_abc123_10_60_v1'`); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "text", modelID, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("embedding: %v", err)
	}

	searcher := pgmedia.NewMediaSearcher(db)

	// Fail-closed: empty workspace, non-system.
	if _, err := searcher.Search(ctx, searchRequestForWorkspace("", "celebrity", []float32{1, 0, 0, 0})); err == nil {
		t.Fatal("expected fail-closed error for empty workspace")
	}
	// Fail-closed: reserved sentinel.
	if _, err := searcher.Search(ctx, searchRequestForWorkspace("default", "celebrity", []float32{1, 0, 0, 0})); err == nil {
		t.Fatal("expected fail-closed error for reserved workspace sentinel")
	}
	// Workspace isolation: ws-b sees nothing from ws-a.
	results, err := searcher.Search(ctx, searchRequestForWorkspace("ws-b", "celebrity", []float32{1, 0, 0, 0}))
	if err != nil {
		t.Fatalf("Search ws-b: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("workspace isolation leak: ws-b saw %d rows", len(results))
	}
	// System scope bypasses isolation (admin path).
	sysResults, err := searcher.Search(ctx, pgmedia.SystemSearchRequest([]float32{1, 0, 0, 0}))
	if err != nil {
		t.Fatalf("Search system: %v", err)
	}
	if len(sysResults) != 1 {
		t.Fatalf("system scope results = %d, want 1", len(sysResults))
	}
}

// TestCutover_DuplicateCommitIsIdempotent pins upsert idempotency:
// re-writing the same (asset, type, model) embedding keeps exactly one
// row and overwrites the vector; re-committing the asset keeps one row.
func TestCutover_DuplicateCommitIsIdempotent(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	modelID := "test-siglip-v1"
	if err := w.RegisterEmbeddingFamily(ctx, "visual", modelID, 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}

	for i := 0; i < 2; i++ {
		res, err := c.CommitMediaAsset(ctx, fullCommitRequest())
		if err != nil {
			t.Fatalf("commit #%d: %v", i+1, err)
		}
		if i == 0 && !res.Created {
			t.Fatal("first commit should be Created")
		}
		if i == 1 && res.Created {
			t.Fatal("second commit should NOT be Created (idempotent)")
		}
		if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "visual", modelID, []float32{0.5, 0.5, 0.5, 0.5}); err != nil {
			t.Fatalf("embedding #%d: %v", i+1, err)
		}
		if err := w.UpsertAssetFeatures(ctx, pgmedia.AssetFeatureRecord{AssetID: "yt_abc123_10_60_v1"}); err != nil {
			t.Fatalf("features #%d: %v", i+1, err)
		}
	}

	if n := countWhere(t, db, "media_embeddings", "asset_id", "yt_abc123_10_60_v1"); n != 1 {
		t.Fatalf("embedding rows = %d, want 1 (idempotent)", n)
	}
	if n := countWhere(t, db, "media_asset_features", "asset_id", "yt_abc123_10_60_v1"); n != 1 {
		t.Fatalf("feature rows = %d, want 1 (idempotent)", n)
	}
}

// TestCutover_EmbeddingModelVersionPreserved pins the family contract:
// two incompatible model families for the same asset coexist under
// distinct (embedding_type, model_id) keys — model identity is never
// lost on re-upsert.
func TestCutover_EmbeddingModelVersionPreserved(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	c, _ := newPostgresCommitter(t)

	if err := w.RegisterEmbeddingFamily(ctx, "visual", "siglip-v1", 4); err != nil {
		t.Fatalf("register siglip-v1: %v", err)
	}
	if err := w.RegisterEmbeddingFamily(ctx, "visual", "siglip-v2", 8); err != nil {
		t.Fatalf("register siglip-v2: %v", err)
	}
	if _, err := c.CommitMediaAsset(ctx, fullCommitRequest()); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Different model versions coexist; each keeps its own vector.
	if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "visual", "siglip-v1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("upsert v1: %v", err)
	}
	if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "visual", "siglip-v2", []float32{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}); err != nil {
		t.Fatalf("upsert v2: %v", err)
	}
	// Re-upserting v1 does not clobber v2 (identity = (asset, type, model)).
	if err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "visual", "siglip-v1", []float32{0, 1, 0, 0}); err != nil {
		t.Fatalf("re-upsert v1: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_embeddings WHERE asset_id = $1`, "yt_abc123_10_60_v1").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("embedding rows = %d, want 2 (both model versions preserved)", n)
	}
	var dim int
	if err := db.QueryRow(`SELECT vector_dims(embedding) FROM media_embeddings WHERE asset_id = $1 AND model_id = 'siglip-v2'`, "yt_abc123_10_60_v1").Scan(&dim); err != nil {
		t.Fatalf("dims: %v", err)
	}
	if dim != 8 {
		t.Fatalf("siglip-v2 dim = %d, want 8", dim)
	}
}

// TestCutover_FamilyGateRejectsDimensionMismatch pins the fail-closed
// trigger: a registered 4-dim family rejects an 8-dim vector.
func TestCutover_FamilyGateRejectsDimensionMismatch(t *testing.T) {
	w, _ := newVectorWriter(t)
	ctx := context.Background()
	if err := w.RegisterEmbeddingFamily(ctx, "visual", "strict-model", 4); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := w.UpsertEmbedding(ctx, "yt_abc123_10_60_v1", "visual", "strict-model", make([]float32, 8))
	if err == nil {
		t.Fatal("expected fail-closed error for dimension mismatch")
	}
	if !strings.Contains(err.Error(), "media_embeddings") && !strings.Contains(err.Error(), "vector dim") {
		t.Fatalf("unexpected error surface: %v", err)
	}
}

// TestCutover_ActiveFamilyResolution pins the search-side family lookup:
// exactly one registered family resolves; zero families fail closed.
func TestCutover_ActiveFamilyResolution(t *testing.T) {
	w, db := newVectorWriter(t)
	ctx := context.Background()

	if _, _, err := w.ActiveEmbeddingFamily(ctx, "visual"); err == nil {
		t.Fatal("expected fail-closed error when no family is registered")
	}
	if err := w.RegisterEmbeddingFamily(ctx, "visual", "prod-model", 768); err != nil {
		t.Fatalf("register: %v", err)
	}
	modelID, dim, err := w.ActiveEmbeddingFamily(ctx, "visual")
	if err != nil {
		t.Fatalf("ActiveEmbeddingFamily: %v", err)
	}
	if modelID != "prod-model" || dim != 768 {
		t.Fatalf("family = (%s, %d), want (prod-model, 768)", modelID, dim)
	}
	_ = db
}

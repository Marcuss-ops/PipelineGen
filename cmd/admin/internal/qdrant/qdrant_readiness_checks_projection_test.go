package qdrant

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// openProjectionTestDB builds a media_assets table with the canonical
// taxonomy columns the eligibility SSOT reads (openTestDB's minimal
// fixture does not carry them).
func openProjectionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openTestDB(t)
	_, err := db.Exec(`ALTER TABLE media_assets ADD COLUMN deleted_at TEXT`)
	require.NoError(t, err)
	return db
}

// insertEligibleAsset inserts a fully-eligible row (video/clip with
// taxonomy + populated embedding).
func insertEligibleAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets
		(id, media_type, asset_kind, namespace, source_type, deleted_at, embedding_json)
		VALUES (?, 'video', 'clip', 'stock', 'youtube', NULL, '[0.1,0.2]')`, id)
	require.NoError(t, err)
}

// insertIneligibleAsset inserts a row that fails the eligibility SSOT
// (audio media type — never searchable).
func insertIneligibleAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets
		(id, media_type, asset_kind, namespace, source_type, deleted_at, embedding_json)
		VALUES (?, 'audio', 'voiceover', 'audio', 'drive', NULL, '[0.1,0.2]')`, id)
	require.NoError(t, err)
}

// mockReadinessQdrant serves the /aliases + scroll surface with the
// given points (each a payload asset_id).
func mockReadinessQdrant(t *testing.T, points []string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	served := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"aliases": []map[string]any{{
					"alias_name":      "media_assets_current",
					"collection_name": "media_assets_v4_test",
				}}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v4_test/points/scroll":
			if served {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{"points": []any{}, "next_page_offset": nil},
				})
				return
			}
			served = true
			pts := make([]map[string]any, 0, len(points))
			for _, id := range points {
				pts = append(pts, map[string]any{
					"id":      schema.AssetIDToQdrantPointID(id),
					"payload": map[string]any{"asset_id": id, "name": "n", "source": "youtube"},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points": pts, "next_page_offset": nil},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func readinessDepsWithQdrant(t *testing.T, db *sql.DB, qdrantURL string) readinessDeps {
	t.Helper()
	cfg := validCfg()
	cfg.Qdrant.BaseURL = qdrantURL
	cfg.Qdrant.Enabled = true
	return readinessDeps{DB: db, Cfg: cfg, Log: zap.NewNop()}
}

// TestCheckProjectionParity_Pass — eligible set exactly matches the
// active projection → PASS (0 missing, 0 orphan).
func TestCheckProjectionParity_Pass(t *testing.T) {
	t.Parallel()
	db := openProjectionTestDB(t)
	defer db.Close()
	insertEligibleAsset(t, db, "a1")
	insertIneligibleAsset(t, db, "audio-1") // must NOT count as eligible
	srv := mockReadinessQdrant(t, []string{"a1"})
	defer srv.Close()

	res := checkProjectionParity(context.Background(), readinessDepsWithQdrant(t, db, srv.URL))
	assert.True(t, res.Pass, "eligible==active (0 missing, 0 orphan) must pass; got: %s", res.Err)
}

// TestCheckProjectionParity_MissingFails — an eligible asset absent
// from the active projection fails with the counts in the message.
func TestCheckProjectionParity_MissingFails(t *testing.T) {
	t.Parallel()
	db := openProjectionTestDB(t)
	defer db.Close()
	insertEligibleAsset(t, db, "a1")
	insertEligibleAsset(t, db, "a2")
	srv := mockReadinessQdrant(t, []string{"a1"}) // a2 missing
	defer srv.Close()

	res := checkProjectionParity(context.Background(), readinessDepsWithQdrant(t, db, srv.URL))
	assert.False(t, res.Pass)
	assert.Contains(t, res.Err, "missing_in_qdrant=1")
	assert.Contains(t, res.Err, "eligible_sqlite=2")
}

// TestCheckProjectionParity_OrphanFails — a stale point in the active
// projection fails (orphan != 0).
func TestCheckProjectionParity_OrphanFails(t *testing.T) {
	t.Parallel()
	db := openProjectionTestDB(t)
	defer db.Close()
	insertEligibleAsset(t, db, "a1")
	srv := mockReadinessQdrant(t, []string{"a1", "stale-9"})
	defer srv.Close()

	res := checkProjectionParity(context.Background(), readinessDepsWithQdrant(t, db, srv.URL))
	assert.False(t, res.Pass)
	assert.Contains(t, res.Err, "orphan_in_qdrant=1")
}

// TestCheckProjectionParity_QdrantDisabledFails — the gate fails
// closed when qdrant.enabled=false.
func TestCheckProjectionParity_QdrantDisabledFails(t *testing.T) {
	t.Parallel()
	db := openProjectionTestDB(t)
	defer db.Close()
	cfg := validCfg()
	cfg.Qdrant.Enabled = false
	res := checkProjectionParity(context.Background(), readinessDeps{DB: db, Cfg: cfg, Log: zap.NewNop()})
	assert.False(t, res.Pass)
	assert.Contains(t, res.Err, "qdrant.enabled=false")
}

// TestProbeProjectionParity_PopulatesReport — the orchestrator helper
// fills the report parity fields + the projection_parity check entry.
func TestProbeProjectionParity_PopulatesReport(t *testing.T) {
	t.Parallel()
	db := openProjectionTestDB(t)
	defer db.Close()
	insertEligibleAsset(t, db, "a1")
	srv := mockReadinessQdrant(t, []string{"a1"})
	defer srv.Close()

	report := &qdrantReadinessReport{Checks: make(map[string]string)}
	probeProjectionParity(context.Background(), readinessDepsWithQdrant(t, db, srv.URL), report)

	assert.Equal(t, "pass", report.Checks["projection_parity"])
	assert.Equal(t, 1, report.ProjectionEligibleSQLite)
	assert.Equal(t, 1, report.ProjectionQdrantPoints)
	assert.Equal(t, 0, report.ProjectionMissingCount)
	assert.Equal(t, 0, report.ProjectionOrphanCount)
}

package clipindexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

type mockVectorStoreIndexer struct {
	indexedIDs []string
}

func (m *mockVectorStoreIndexer) UpsertFromClip(ctx context.Context, clipID string) error {
	m.indexedIDs = append(m.indexedIDs, clipID)
	return nil
}

func (m *mockVectorStoreIndexer) UpsertFromClips(ctx context.Context, clipIDs []string) error {
	m.indexedIDs = append(m.indexedIDs, clipIDs...)
	return nil
}

func TestIndexingDoesNotSpawnPythonPerClip(t *testing.T) {
	// 1. Create in-memory SQLite DB with the inline 10-column schema.
	//
	// CANONICAL-DRIFT-MIG094 closure (June 2026, July 2026 follow-on):
	// this test is INTENTIONALLY EXEMPT from the canonical.go "MUST
	// embed" rule. A fold attempt (replacing this inline block with
	// drive.CanonicalMediaAssetsSchema) was attempted in the
	// 9912a118-era closure pass, validated against this test, and
	// REVERTED because:
	//
	//   * The inline schema declares `embedding_json TEXT` (nullable);
	//     the test inserts raw SQL NULL into it.
	//   * Canonical declares `embedding_json TEXT NOT NULL DEFAULT '[]'`;
	//     the fold required the test to either omit the column (yielding
	//     DEFAULT '[]') or pass a non-NULL value. Both options broke the
	//     test: the indexer's CAS check in setIndexedAt rejects rows
	//     where embedding_json is non-NULL, interpreting the value as
	//     `already indexed` (the `source_version=""` CAS-miss surface).
	//
	// The clause "Fixtures that need an EXACT column-count or a
	// semantic-test-contract inline schema are exempt" in
	// canonical.go's header doc lists clipindexer/service_test.go as a
	// documented exemption alongside the 3 other exempt fixtures
	// (clips_crud_test, images_repository_test, clip_atomic_writer_test).
	// PR-CLIPINDEXER-FOLD-INVESTIGATE forward-pointer carries the
	// investigation into the embedding_json NOT-NULL / CAS contract.
	db := drive.NewTestDBWithSchema(t, `
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			name TEXT,
			source TEXT,
			tags TEXT,
			embedding_json TEXT,
			metadata_json TEXT,
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			index_state_updated_at TEXT NOT NULL DEFAULT '',
			source_version TEXT NOT NULL DEFAULT '',
			file_hash TEXT NOT NULL DEFAULT ''
		)
	`)
	defer db.Close()

	// Insert test clips. The fold from the previous inline schema
	// (embedding_json TEXT, nullable) to CanonicalMediaAssetsSchema
	// (embedding_json TEXT NOT NULL DEFAULT '[]') means we can no
	// longer insert raw NULL; the canonical DEFAULT absorbs the
	// omission. We therefore omit embedding_json from the column
	// list rather than passing NULL — the canonical contract enforces
	// NOT NULL with default, and the test contract is independent of
	// the embedding_json payload (the indexer is the unit under test,
	// not the embedding column).
	_, err := db.Exec(`
		INSERT INTO media_assets (id, name, source, tags, metadata_json)
		VALUES
			('clip_1', 'Test Clip One', 'artlist', '[]', '{"local_path":"/data/clip1.mp4","search_text":"test clip one"}'),
			('clip_2', 'Test Clip Two', 'artlist', '[]', '{"local_path":"/data/clip2.mp4","search_text":"test clip two"}')
	`)
	require.NoError(t, err)

	// 3. Setup Mock HTTP Server to mock embedding_server.py
	var apiCalled int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/index" {
			apiCalled++
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"status":     "success",
				"clip_id":    "clip_1",
				"embedding":  []float64{0.1, 0.2, 0.3},
				"dimensions": 3,
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// 4. Create Service.
	cfg := &Config{
		Enabled:    true,
		ServerURL:  server.URL,
		PythonBin:  "python-invalid-should-not-be-called", // if subprocess is spawned, it will fail
		ScriptPath: "scripts/bridges/index_clips.py",
	}
	//
	// PG-016 typed-handle migration (June 2026): clipindexer.NewService now
	// accepts *storage.SQLiteDB; wrap the test fixture's *sql.DB (returned by
	// drive.NewTestDBWithSchema) into the typed handle. The body uses
	// method promotion transparently.
	svc := NewService(cfg, &drive.SQLiteDB{DB: db}, ":memory:", zap.NewNop())
	vs := &mockVectorStoreIndexer{}
	svc.vectorStore = vs

	// 5. Index individual clip via API
	err = svc.IndexClip(context.Background(), "clip_1")
	require.NoError(t, err)
	assert.Equal(t, 1, apiCalled)


}

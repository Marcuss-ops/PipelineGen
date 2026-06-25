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
	// 1. Create in-memory SQLite DB with schema.
	//
	// QDRANT-002 PR6: the canonical index_state column is now a
	// first-class SQL column on media_assets (migration 094). The
	// indexer writers read/write it directly, so the test schema
	// MUST include the column or setIndexedAt fails with
	// "no such column: index_state".
	db := drive.NewTestDBWithSchema(t, `
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			name TEXT,
			source TEXT,
			tags TEXT,
			embedding_json TEXT,
			metadata_json TEXT,
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			index_state_updated_at TEXT NOT NULL DEFAULT ''
		)
	`)
	defer db.Close()

	// Insert test clips
	_, err := db.Exec(`
		INSERT INTO media_assets (id, name, source, tags, embedding_json, metadata_json)
		VALUES 
			('clip_1', 'Test Clip One', 'artlist', '[]', NULL, '{"local_path":"/data/clip1.mp4","search_text":"test clip one"}'),
			('clip_2', 'Test Clip Two', 'artlist', '[]', NULL, '{"local_path":"/data/clip2.mp4","search_text":"test clip two"}')
	`)
	require.NoError(t, err)

	// 3. Setup Mock HTTP Server to mock embedding_server.py
	var apiCalled int
	var bulkCalled int
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
		} else if r.URL.Path == "/index_bulk" {
			bulkCalled++
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"count":  2,
				"results": []map[string]any{
					{"clip_id": "clip_1", "embedding": []float64{0.1, 0.2, 0.3}},
					{"clip_id": "clip_2", "embedding": []float64{0.4, 0.5, 0.6}},
				},
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

	// 6. Index run items in bulk via API
	items := []map[string]any{
		{"clip_id": "clip_1"},
		{"clip_id": "clip_2"},
	}
	err = svc.IndexRunItems(context.Background(), items)
	require.NoError(t, err)
	assert.Equal(t, 1, bulkCalled)
}

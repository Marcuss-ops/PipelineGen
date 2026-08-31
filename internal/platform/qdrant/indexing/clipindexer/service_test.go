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

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
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

// seedLegacyFileMD5 pre-computes the contentHash that IndexClip will compute
// when called, and writes it to media_assets.file_hash so the CAS fence
// in setIndexedAt passes for the test fixture.
//
// Why this is necessary (PR-CLIPINDEXER-FOLD-INVESTIGATE closure):
//
//	The CAS fence matches (id, source_version, file_hash, index_state='INDEXING').
//	A test row with file_hash='' (canonical default) and source_version=''
//	(canonical default) CAS-misses whenever computeContentHash returns a
//	non-empty hash — production callers always pre-populate file_hash via
//	DownloadProcessor / Drive upload / cmd/admin/backfill_hash.go before
//	calling IndexClip, so this seed mirrors the production setup shape.
//
//	Computing the hash via svc.computeContentHash (rather than duplicating
//	the hashing algorithm here) means algorithm evolution (e.g. adding a
//	new field to contentParts) automatically stays in lockstep; the test
//	cannot drift from production by a missing input variable.
//
// Mirrors the `preSeedFileHash` helper in indexing_api_audio_test.go
// (already shipped on origin/main, July 2026, PRE-AUDIO-CHANNEL-EXTENSION)
// — both helpers thread (svc, clipID) and write back to the same row.
func seedLegacyFileMD5(t *testing.T, svc *Service, clipID string) {
	t.Helper()
	ctx := context.Background()
	ch, _, err := svc.computeContentHash(ctx, clipID)
	if err != nil {
		t.Fatalf("seedLegacyFileMD5: computeContentHash for %s failed: %v", clipID, err)
	}
	if _, err := svc.db.ExecContext(ctx,
		`UPDATE media_assets SET file_hash = ? WHERE id = ?`, ch, clipID,
	); err != nil {
		t.Fatalf("seedLegacyFileMD5: UPDATE for %s failed: %v", clipID, err)
	}
}

func TestIndexingDoesNotSpawnPythonPerClip(t *testing.T) {
	// 1. Create in-memory SQLite DB with the canonical media_assets schema.
	//
	// Closed via PR-CLIPINDEXER-FOLD-INVESTIGATE (July 2026): the inline
	// 10-col schema previously used here was a strict subset of canonical
	// but INADVERTENTLY PASSED the CAS fence because it lacked the
	// search_text column — computeContentHash errored with `no such
	// column` and fell back to contentHash=""; the empty hash then
	// matched row.file_hash='' (canonical default) and the test passed
	// by accident. The fold + the seedLegacyFileMD5 helper (above) now exercise
	// the production-shape CAS fence honestly: search_text column exists
	// in canonical, computeContentHash succeeds, file_hash is pre-seeded
	// with the same hash via seedLegacyFileMD5, CAS matches, setIndexedAt
	// writes INDEXED. See canonical.go header's "Historical audits" block
	// for the full exemption-archaeology context.
	db := drive.NewMigratedTestDB(t)
	defer db.Close()

	// 2. Insert test clips. embedding_json is OMITTED from the column
	// list so canonical's NOT NULL DEFAULT '[]' absorbs the omission
	// (the test contract is the indexer, not the embedding column).
	// search_text column is also omitted — the column-level default is
	// '', and the test's metadata_json carries $.search_text which is
	// read by indexTextViaAPI via json_extract. computeContentHash will
	// see an empty search_text column though, meaning its returned hash
	// composes only with name + transcript — which is exactly the
	// production-shape pattern (search_text starts empty until the
	// outbox elsepath populates it).
	//
	// namespace is set explicitly (matching the artlist provider): this
	// fixture runs on the CANONICAL migrated schema (NewMigratedTestDB),
	// so the taxonomy gate (ResolveIndexEligibility → AssetTaxonomy.
	// Validate) demands a non-empty namespace. Without it the row is
	// REGISTERED-not-searchable and IndexAsset skips the embedding call
	// entirely — the honest production-shape eligibility path is exactly
	// what this test must exercise.
	_, err := db.Exec(`
		INSERT INTO media_assets (id, name, source, media_type, asset_kind, source_type, namespace, tags, metadata_json, lifecycle_state, index_state)
		VALUES
			('clip_1', 'Test Clip One', 'artlist', 'video', 'stock_video', 'artlist', 'artlist', '[]', '{"local_path":"/data/clip1.mp4","search_text":"test clip one"}', 'ACTIVE', 'DISCOVERED'),
			('clip_2', 'Test Clip Two', 'artlist', 'video', 'stock_video', 'artlist', 'artlist', '[]', '{"local_path":"/data/clip2.mp4","search_text":"test clip two"}', 'ACTIVE', 'DISCOVERED')
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
	svc.SetAssetMutationCommitter(newTestAssetMutationCommitter(db))
	vs := &mockVectorStoreIndexer{}
	svc.vectorStore = vs

	// 4.5. PR-CLIPINDEXER-FOLD-INVESTIGATE seed step: production callers
	// always pre-populate media_assets.file_hash (via DownloadProcessor
	// / Drive upload / cmd/admin/backfill_hash.go admin) before calling
	// IndexClip. The CAS fence (setIndexedAt) matches (id, source_version,
	// file_hash, index_state='INDEXING'); without the seed, the empty
	// canonical-default file_hash mismatches the contentHash that
	// computeContentHash returns for a row with name='Test Clip One',
	// and setIndexedAt surfaces ErrIndexSuperseded
	// (CAS miss for clip_1 (source_version="") — index event superseded
	// by newer version).
	seedLegacyFileMD5(t, svc, "clip_1")

	// 5. Index individual clip via API
	err = svc.IndexClip(context.Background(), "clip_1")
	require.NoError(t, err)
	assert.Equal(t, 1, apiCalled)
}

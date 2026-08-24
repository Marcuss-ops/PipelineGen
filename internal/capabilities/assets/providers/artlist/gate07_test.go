// Package artlist — Gate 07 Search Tests (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-07-SEARCH-INDEXED: verify that DBSearcher finds
// clips that have reached index_state=INDEXED (the contract that
// downstream search consumers rely on) AND that DBSearcher does NOT
// filter by index_state (so partial-indexed or pre-indexed clips are
// still discoverable for operators inspecting the pipeline).
//
// godlike/07 no-fake-availability: the tests query the canonical
// media_assets + clip_search_terms tables directly. The search results
// are cross-validated against the SQLite row to confirm source=artlist
// and media_type=video on every hit.
//
// godlike/06 SSOT: the canonical Searcher/DBSearcher port lives in
// this package; SearchRequest.Term is the canonical query. The index_state
// column (not metadata_json.$.index_state) is the canonical source of
// truth after migration 094; the tests below work against the
// metadata_json-shaped value because artlist's test schema pre-dates
// migration 094 and inserts via SetMetadataString("index_state", ...).
//
// Test-double strategy (per user directive: "riusa i test doubles esistenti"):
//   - successMediaProcessor (gate01_happy_path_test.go)
//   - stubDispatcherForArtlist (dispatcher_stub_test.go)
//   - stubRunRepoForArtlist (dispatcher_stub_test.go)
//   - No new test doubles added — gate07 reuses the same hermetic stack
//     as gate06 + gate08. Test 1 simulates the index_state=INDEXED
//     transition via a SQL UPDATE (same pattern as gate06). Test 2
//     inserts clips with mixed index_states directly (no RunTag needed)
//     to assert the DBSearcher contract.
package artlist

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ────────────────────────────────────────────────────────────
// Gate 07: Search — finds INDEXED clips; does not filter by index_state
// ────────────────────────────────────────────────────────────

// TestGate07_SearchFindsIndexedClips verifies the search-finds-indexed
// contract (Gate 07 of ARTLIST-DOD-2026-07-07):
//
//  1. After a successful RunTag + transition to index_state=INDEXED,
//     DBSearcher.Search with the same term returns the processed clips.
//  2. Every returned candidate matches the processed clip IDs (no false
//     positives, no missing clips).
//  3. Every returned candidate's SQLite row has index_state='INDEXED'.
//  4. Every returned candidate's SQLite row has source='artlist' and
//     media_type='video' (preserved from RunTag).
//
// This is the contract that downstream /api/media/search consumers
// rely on: an Artlist clip that has been processed AND indexed in
// Qdrant must be discoverable by term search.
func TestGate07_SearchFindsIndexedClips(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	const searchTerm = "gate07idx"
	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate07-idx-1", Title: "Indexed Search 1", SourceRef: "https://cdn.artlist.io/video/gate07-i1.m3u8", PageURL: "https://artlist.io/clip/idx-1"},
		{ID: "gate07-idx-2", Title: "Indexed Search 2", SourceRef: "https://cdn.artlist.io/video/gate07-i2.m3u8", PageURL: "https://artlist.io/clip/idx-2"},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	clips := []struct {
		id, name, sourceURL string
	}{
		{"gate07-idx-1", "Indexed Search 1", "https://cdn.artlist.io/video/gate07-i1.m3u8"},
		{"gate07-idx-2", "Indexed Search 2", "https://cdn.artlist.io/video/gate07-i2.m3u8"},
	}
	for _, clip := range clips {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", searchTerm, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &successMediaProcessor{}

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Domain: ArtlistDomainDeps{
				MediaProcessor: processor,
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	// Step 1: RunTag processes the 2 clips.
	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         searchTerm,
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate07-idx-root",
	})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Processed)

	// Step 2: Simulate the Qdrant indexing flow (DISCOVERED → INDEXED).
	for _, clipID := range []string{"gate07-idx-1", "gate07-idx-2"} {
		_, err := db.Exec(
			`UPDATE media_assets
			 SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.index_state', 'INDEXED')
			 WHERE id = ?`,
			clipID,
		)
		require.NoError(t, err)
	}

	// Step 3: DBSearcher must find the INDEXED clips.
	dbSearcher := NewDBSearcher(artlistRepo)
	candidates, err := dbSearcher.Search(ctx, SearchRequest{
		Term:  searchTerm,
		Limit: 10,
	})
	require.NoError(t, err)

	// ── Gate 07: Contract 1 — search returns the expected INDEXED clips ──
	assert.Equal(t, 2, len(candidates),
		"search must return exactly 2 INDEXED clips for term %q", searchTerm)

	foundIDs := map[string]bool{}
	for _, c := range candidates {
		foundIDs[c.ID] = true
		t.Logf("search hit: id=%s title=%s source_ref=%s", c.ID, c.Title, c.SourceRef)

		assert.NotEmpty(t, c.ID, "candidate ID must be non-empty")
		assert.NotEmpty(t, c.Title, "candidate Title must be non-empty")
	}
	assert.True(t, foundIDs["gate07-idx-1"], "search must find gate07-idx-1 (INDEXED)")
	assert.True(t, foundIDs["gate07-idx-2"], "search must find gate07-idx-2 (INDEXED)")

	// ── Gate 07: Contract 2 — every hit's SQLite row is INDEXED + artlist + video ──
	for _, clipID := range []string{"gate07-idx-1", "gate07-idx-2"} {
		var source, mediaType, idxState string
		err := db.QueryRow(
			`SELECT source, media_type, json_extract(metadata_json, '$.index_state')
			 FROM media_assets WHERE id = ?`,
			clipID,
		).Scan(&source, &mediaType, &idxState)
		require.NoError(t, err, "clip %s must exist in media_assets", clipID)

		assert.Equal(t, "INDEXED", idxState,
			"clip %s: index_state must be 'INDEXED' (search hit on INDEXED row)", clipID)
		assert.Equal(t, "artlist", source,
			"clip %s: source must be 'artlist' (preserved from RunTag)", clipID)
		assert.Equal(t, "video", mediaType,
			"clip %s: media_type must be 'video' (preserved from RunTag)", clipID)
	}

	t.Logf("Gate 07: SearchFindsIndexedClips verified — search for %q returns both INDEXED artlist/video clips", searchTerm)
}

// TestGate07_DBSearcherDoesNotFilterByIndexState verifies the
// negative-positive contract (Gate 07 of ARTLIST-DOD-2026-07-07):
//
//  1. DBSearcher.Search returns clips regardless of their index_state
//     (DISCOVERED, INDEXING, INDEXED, INDEXING_FAILED are all findable).
//  2. The search results contain the union of all clips tagged with
//     the term, not just the INDEXED subset.
//  3. The DBSearcher source-of-truth is the clip_search_terms JOIN
//     media_assets table — not the index_state column.
//
// This is the contract that operator dashboards and pipeline inspectors
// rely on: a clip that is mid-pipeline (e.g., DISCOVERED → EMBEDDING →
// INDEXING) must still be visible to search so operators can debug
// "why is my clip not appearing in production search?" without having
// to query SQLite directly.
//
// The test inserts 4 clips with mixed index_states (DISCOVERED,
// INDEXING, INDEXED, INDEXING_FAILED) tagged with the same term, then
// asserts DBSearcher returns ALL 4 of them — not just the INDEXED one.
func TestGate07_DBSearcherDoesNotFilterByIndexState(t *testing.T) {
	ctx := context.Background()

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	const searchTerm = "mixedstates"

	// 4 clips tagged with the same search term, with deliberately
	// mixed index_states to verify DBSearcher doesn't filter on
	// index_state. Production-equivalent pre-population is via
	// SearchLiveAndSave, but for this contract test we insert
	// directly to control the index_state of each row.
	clips := []struct {
		id, name, sourceURL, idxState string
	}{
		{"gate07-mx-1", "Mixed State DISCOVERED", "https://cdn.artlist.io/video/gate07-m1.m3u8", string(asset.StateDiscovered)},
		{"gate07-mx-2", "Mixed State INDEXING", "https://cdn.artlist.io/video/gate07-m2.m3u8", string(asset.StateIndexing)},
		{"gate07-mx-3", "Mixed State INDEXED", "https://cdn.artlist.io/video/gate07-m3.m3u8", string(asset.StateIndexed)},
		{"gate07-mx-4", "Mixed State INDEXING_FAILED", "https://cdn.artlist.io/video/gate07-m4.m3u8", string(asset.StateIndexingFailed)},
	}
	for _, clip := range clips {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", searchTerm, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", clip.idxState)
		insertTestClip(t, db, a)
	}

	// Run DBSearcher directly. No RunTag needed — the test focuses on
	// the DBSearcher contract, not the RunTag→Qdrant pipeline.
	dbSearcher := NewDBSearcher(artlistRepo)
	candidates, err := dbSearcher.Search(ctx, SearchRequest{
		Term:  searchTerm,
		Limit: 50,
	})
	require.NoError(t, err)

	// ── Gate 07: Contract 1 — DBSearcher returns ALL 4 mixed-state clips ──
	require.Equal(t, 4, len(candidates),
		"DBSearcher must return all 4 clips regardless of index_state — search must NOT filter by index_state")

	foundIDs := map[string]bool{}
	for _, c := range candidates {
		foundIDs[c.ID] = true
	}

	// Each of the 4 mixed-state clips must be findable.
	assert.True(t, foundIDs["gate07-mx-1"], "DBSearcher must find DISCOVERED clip (gate07-mx-1)")
	assert.True(t, foundIDs["gate07-mx-2"], "DBSearcher must find INDEXING clip (gate07-mx-2)")
	assert.True(t, foundIDs["gate07-mx-3"], "DBSearcher must find INDEXED clip (gate07-mx-3)")
	assert.True(t, foundIDs["gate07-mx-4"], "DBSearcher must find INDEXING_FAILED clip (gate07-mx-4)")

	// ── Gate 07: Contract 2 — DBSearcher must NOT mutate the row's index_state ──
	// Cross-validate that each clip's SQLite row still carries the
	// original index_state we inserted (i.e., DBSearcher doesn't
	// re-project the row to INDEXED on read). A single bulk query is
	// sufficient — we already verified all 4 IDs are in foundIDs above.
	originalStates := map[string]string{
		"gate07-mx-1": string(asset.StateDiscovered),
		"gate07-mx-2": string(asset.StateIndexing),
		"gate07-mx-3": string(asset.StateIndexed),
		"gate07-mx-4": string(asset.StateIndexingFailed),
	}
	for clipID, originalState := range originalStates {
		var dbIdx string
		err := db.QueryRow(
			`SELECT json_extract(metadata_json, '$.index_state') FROM media_assets WHERE id = ?`,
			clipID,
		).Scan(&dbIdx)
		require.NoError(t, err, "clip %s must still be in media_assets", clipID)
		assert.Equal(t, originalState, dbIdx,
			"clip %s: SQLite index_state must be preserved (%s) — DBSearcher must not mutate the row on read",
			clipID, originalState)
	}

	// Optional sanity: dump the result IDs as JSON for human inspection.
	idDump, _ := json.Marshal(foundIDs)
	t.Logf("Gate 07: DBSearcher does NOT filter by index_state — found 4/4 mixed-state clips: %s", string(idDump))
}

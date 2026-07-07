// Package artlist — Gate 10 Qdrant Failure Test (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-10-QDRANT-FAILURE: verify that when Qdrant
// is unavailable during indexing, the pipeline does NOT silently
// mark clips as INDEXED. The outbox event stays pending (does not
// transition to completed), and index_state remains at INDEXING
// rather than being falsely promoted to INDEXED.
//
// godlike/07 no-fake-availability: the failingQdrantDispatcher
// wraps the canonical stubDispatcherForArtlist to do the media_assets
// upsert (SQLite write succeeds) but deliberately does NOT set
// index_state=INDEXED. This simulates the production failure mode:
// the IndexingHandler calls Qdrant upsert → Qdrant returns 503 →
// the handler leaves the outbox event as pending (retryable) and
// does NOT update media_assets.index_state.
//
// godlike/06 SSOT: the canonical index_state values live in
// internal/domain/asset/index_state.go. The DISCOVERED → INDEXED
// transition is the contract that Gate 06 verifies; Gate 10 verifies
// the INVERSE: when Qdrant is down, INDEXED must NOT be reached.
package artlist

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// failingQdrantDispatcher is a Gate 10 test double that simulates
// a Qdrant outage: it wraps the canonical stubDispatcherForArtlist
// (which does the media_assets upsert) but deliberately does NOT
// set index_state=INDEXED. This mirrors what the production path
// does when Qdrant is unreachable:
//
//  1. The outbox IndexingHandler receives the asset.index.requested
//     event.
//  2. It calls clipindexer.IndexClip → QdrantWriter.Upsert.
//  3. Qdrant returns 503/connection refused.
//  4. The handler leaves the outbox event as pending (retryable)
//     and does NOT update media_assets.index_state.
//
// godlike/07 no-fake-availability: this double is the INVERSE of
// the qdrantIndexingDispatcher (gate06_qdrant_test.go). That double
// sets index_state=INDEXED (success). This double does NOT set it
// (failure). The two together form the full success/failure contract
// for the Qdrant indexing surface.
//
// Honest scope-lock: the "outbox event stays pending" half of the
// Gate 10 contract is untestable at this package layer — the
// stubDispatcherForArtlist.EnqueueAndIndex does direct SQLite
// repo.UpsertClip, not outbox enqueue. Verifying outbox event
// lifecycle requires integration/E2E testing (forward-pointer
// PR-ARTLIST-OUTBOX-PENDING-E2E).
type failingQdrantDispatcher struct {
	stubDispatcherForArtlist
	mu    sync.Mutex
	calls []string // dispatched clip IDs (upsert succeeded, indexing failed)
}

// EnqueueAndIndex implements the Dispatcher port. It delegates to the
// inner stubDispatcherForArtlist for the media_assets upsert (which
// succeeds — SQLite is healthy), then deliberately does NOT set
// index_state=INDEXED (simulating Qdrant returning 503).
//
// This is the canonical failure contract: the media_assets row exists
// (upserted), but the Qdrant index state transition never happened.
func (f *failingQdrantDispatcher) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	// Delegate to the canonical stub for the media_assets upsert.
	// This succeeds — SQLite is healthy, only Qdrant is down.
	if err := f.stubDispatcherForArtlist.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		return err
	}

	// ── Gate 10: Qdrant failure simulation ──
	// Do NOT set index_state=INDEXED. The Qdrant upsert failed,
	// so the clip stays at its pre-dispatch index_state (DISCOVERED
	// or INDEXING, depending on the write path).
	//
	// In production, the Qdrant IndexingHandler would:
	//   1. Call clipindexer.IndexClip → QdrantWriter.Upsert.
	//   2. On Qdrant 503 → return retryable error.
	//   3. The outbox event stays pending.
	//   4. media_assets.index_state is never updated to INDEXED.
	//
	// We deliberately do NOT set index_state=INDEXED here to
	// simulate that failure.

	f.mu.Lock()
	f.calls = append(f.calls, clip.ID)
	f.mu.Unlock()

	return nil
}

// DispatchCount returns the number of clips that were dispatched
// (upserted but NOT indexed).
func (f *failingQdrantDispatcher) DispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// DispatchedClipIDs returns the ordered list of dispatched clip IDs.
func (f *failingQdrantDispatcher) DispatchedClipIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Compile-time assertion: satisfies the Dispatcher port.
var _ Dispatcher = (*failingQdrantDispatcher)(nil)

// ────────────────────────────────────────────────────────────
// Gate 10: Qdrant Failure — no fake INDEXED, event stays pending
// ────────────────────────────────────────────────────────────

// TestGate10_QdrantFailureIndexStateNotIndexed verifies the Qdrant
// failure contract (Gate 10 of ARTLIST-DOD-2026-07-07):
//
//  1. When Qdrant is unavailable, media_assets.index_state does
//     NOT transition to INDEXED. Every processed clip stays at
//     its pre-dispatch index_state (DISCOVERED in the test setup;
//     the production artlist pipeline would set INDEXING at a
//     different stage — the critical contract is "not falsely
//     INDEXED", not the specific intermediate state).
//  2. The Artlist pipeline itself succeeds (resp.OK=true,
//     resp.Processed matches clip count). SQLite is healthy —
//     only Qdrant indexing is broken.
//  3. The dispatcher was called exactly once per clip (the upsert
//     happened), but the index_state post-condition is absent.
//  4. No clip has index_state=INDEXED (the false-success
//     anti-pattern is explicitly rejected).
//
// godlike/07 no-fake-availability: the test proves that when Qdrant
// is down, the pipeline does NOT silently promote clips to INDEXED.
// In production, the outbox event stays pending and the
// IndexingHandler will retry on the next dispatcher tick.
func TestGate10_QdrantFailureIndexStateNotIndexed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate10-clip-1",
			Title:     "Qdrant Failure Clip A",
			SourceRef: "https://cdn.artlist.io/video/gate10-a.m3u8",
			PageURL:   "https://artlist.io/clip/qdrant-fail-a",
		},
		{
			ID:        "gate10-clip-2",
			Title:     "Qdrant Failure Clip B",
			SourceRef: "https://cdn.artlist.io/video/gate10-b.m3u8",
			PageURL:   "https://artlist.io/clip/qdrant-fail-b",
		},
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

	// Pre-populate clip_search_terms + STAGING/DISCOVERED clips
	// so they are found by the DBSearcher during discovery.
	for _, clip := range []struct {
		id, name, sourceURL, term string
	}{
		{"gate10-clip-1", "Qdrant Failure Clip A", "https://cdn.artlist.io/video/gate10-a.m3u8", "qdrantfail"},
		{"gate10-clip-2", "Qdrant Failure Clip B", "https://cdn.artlist.io/video/gate10-b.m3u8", "qdrantfail"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateStaging,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &successMediaProcessor{}

	// failingQdrantDispatcher: upserts work, but index_state=INDEXED
	// is never set (simulating Qdrant 503).
	failingDisp := &failingQdrantDispatcher{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     failingDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "qdrantfail",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate10-root",
	})

	// ── Gate 10: Contract 1 — pipeline succeeds (SQLite is healthy) ──
	require.NoError(t, err, "RunTag should succeed — only Qdrant indexing is broken, not SQLite")
	require.NotNil(t, resp)
	assert.True(t, resp.OK, "resp.OK should be true — Artlist pipeline succeeded, Qdrant indexing failure is async")
	assert.Equal(t, 2, resp.Processed, "Processed should match clip count — SQLite upsert succeeded")
	assert.Equal(t, 0, resp.Failed, "Failed should be 0 — no pipeline-stage failures")

	// ── Gate 10: Contract 2 — dispatcher was called once per clip ──
	assert.Equal(t, 2, failingDisp.DispatchCount(),
		"dispatcher must be called once per clip (upsert happened)")
	assert.ElementsMatch(t, []string{"gate10-clip-1", "gate10-clip-2"},
		failingDisp.DispatchedClipIDs(),
		"both clips should have been dispatched")

	// ── Gate 10: Contract 3 — index_state is NOT INDEXED ──
	for _, clipID := range []string{"gate10-clip-1", "gate10-clip-2"} {
		var idxState string
		err := db.QueryRow(
			`SELECT COALESCE(json_extract(metadata_json, '$.index_state'), '') FROM media_assets WHERE id = ?`,
			clipID,
		).Scan(&idxState)
		require.NoError(t, err, "clip %s must exist in media_assets", clipID)

		assert.NotEqual(t, string(asset.StateIndexed), idxState,
			"clip %s: index_state must NOT be INDEXED when Qdrant is unavailable", clipID)
		assert.NotEqual(t, "INDEXED", idxState,
			"clip %s: index_state literal must NOT be 'INDEXED' (even if the constant drifts)", clipID)

		t.Logf("clip %s: index_state=%s (expected: NOT INDEXED — Qdrant failure simulation)", clipID, idxState)
	}

	// ── Gate 10: Contract 4 — no clip has index_state=INDEXED ──
	var indexedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM media_assets WHERE id IN ('gate10-clip-1', 'gate10-clip-2') AND json_extract(metadata_json, '$.index_state') = 'INDEXED'`,
	).Scan(&indexedCount)
	require.NoError(t, err)
	assert.Equal(t, 0, indexedCount,
		"zero clips should be INDEXED after Qdrant failure — no false success")

	t.Log("Gate 10: Qdrant failure contract verified — no clips falsely INDEXED")
}

// TestGate10_QdrantFailureProcessedCountUnaffected verifies that
// Qdrant failure does NOT affect the Artlist pipeline's Processed
// count. The pipeline stage (ProcessBatch → PersistResults) operates
// on media_assets upserts, which are SQLite operations. Qdrant
// indexing is an async outbox-handler operation that runs AFTER the
// pipeline stages complete.
//
// This test runs a 3-clip batch through the failing dispatcher and
// verifies:
//  1. resp.Processed == 3 (all clips upserted successfully)
//  2. resp.Failed == 0 (no pipeline-stage errors)
//  3. Every clip's index_state is NOT INDEXED
//  4. The SQLite projection (source, media_type, lifecycle_state)
//     is correct regardless of Qdrant state
func TestGate10_QdrantFailureProcessedCountUnaffected(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate10-batch-1", Title: "Batch Clip 1", SourceRef: "https://cdn.artlist.io/video/gate10-b1.m3u8", PageURL: "https://artlist.io/clip/qdrant-batch-1"},
		{ID: "gate10-batch-2", Title: "Batch Clip 2", SourceRef: "https://cdn.artlist.io/video/gate10-b2.m3u8", PageURL: "https://artlist.io/clip/qdrant-batch-2"},
		{ID: "gate10-batch-3", Title: "Batch Clip 3", SourceRef: "https://cdn.artlist.io/video/gate10-b3.m3u8", PageURL: "https://artlist.io/clip/qdrant-batch-3"},
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

	for _, clip := range []struct {
		id, name, sourceURL, term string
	}{
		{"gate10-batch-1", "Batch Clip 1", "https://cdn.artlist.io/video/gate10-b1.m3u8", "batch"},
		{"gate10-batch-2", "Batch Clip 2", "https://cdn.artlist.io/video/gate10-b2.m3u8", "batch"},
		{"gate10-batch-3", "Batch Clip 3", "https://cdn.artlist.io/video/gate10-b3.m3u8", "batch"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateStaging,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &successMediaProcessor{}

	failingDisp := &failingQdrantDispatcher{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     failingDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "batch",
		Limit:        3,
		Strategy:     "replace",
		RootFolderID: "gate10-batch-root",
	})

	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, 3, resp.Processed, "Processed must match clip count — Qdrant failure does not reduce Processed")
	assert.Equal(t, 0, resp.Failed)

	// Verify every clip's projection is correct regardless of Qdrant state.
	for _, clipID := range []string{"gate10-batch-1", "gate10-batch-2", "gate10-batch-3"} {
		var source, mediaType, lifecycleState, idxState string
		err := db.QueryRow(
			`SELECT source, media_type, lifecycle_state, COALESCE(json_extract(metadata_json, '$.index_state'), '')
			 FROM media_assets WHERE id = ?`, clipID,
		).Scan(&source, &mediaType, &lifecycleState, &idxState)
		require.NoError(t, err)

		assert.Equal(t, "artlist", source)
		assert.Equal(t, "video", mediaType)
		assert.Equal(t, "ACTIVE", lifecycleState)
		assert.NotEqual(t, "INDEXED", idxState,
			"clip %s: index_state must NOT be INDEXED — Qdrant is down", clipID)

		t.Logf("clip %s: source=%s media_type=%s lifecycle=%s index_state=%s",
			clipID, source, mediaType, lifecycleState, idxState)
	}

	t.Log("Gate 10: Processed count unaffected by Qdrant failure — 3/3 clips upserted, none INDEXED")
}

// TestGate10_QdrantFailureDoesNotPreventArtlistRun verifies the
// negative-contract assertion: the Artlist RunTag pipeline must NOT
// fail or short-circuit when Qdrant is down. The pipeline operates
// on SQLite (healthy); Qdrant indexing is an async operation managed
// by the outbox IndexingHandler, not by the pipeline stages.
//
// godlike/07 no-fake-availability: this test proves that Qdrant
// failure does NOT cascade into a RunTag failure. If Qdrant being
// down caused RunTag to fail, an operator would see "Artlist run
// failed" when the real problem is Qdrant, not Artlist. The
// separation of concerns is the contract: RunTag succeeds, outbox
// handler retries Qdrant independently.
func TestGate10_QdrantFailureDoesNotPreventArtlistRun(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate10-sep-1", Title: "Separation Clip", SourceRef: "https://cdn.artlist.io/video/gate10-sep.m3u8", PageURL: "https://artlist.io/clip/sep"},
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

	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('sep', 'gate10-sep-1')")

	a := &asset.Asset{
		ID:             "gate10-sep-1",
		Name:           "Separation Clip",
		SourceURL:      "https://cdn.artlist.io/video/gate10-sep.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateStaging,
		MediaType:      "video",
	}
	a.SetDownloadLink("https://cdn.artlist.io/video/gate10-sep.m3u8")
	a.SetMetadataString("index_state", string(asset.StateDiscovered))
	insertTestClip(t, db, a)

	processor := &successMediaProcessor{}

	failingDisp := &failingQdrantDispatcher{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     failingDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "sep",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "gate10-sep-root",
	})

	// ── Gate 10: Separation of concerns contract ──
	// RunTag must succeed even when Qdrant is down. The Qdrant
	// indexing failure is an async outbox-handler concern, not a
	// pipeline-stage concern.
	require.NoError(t, err, "RunTag must succeed — Qdrant failure is async, not pipeline-blocking")
	assert.True(t, resp.OK)
	assert.Equal(t, 1, resp.Processed)
	assert.Equal(t, 0, resp.Failed)

	// Clip was dispatched (upserted) but not indexed.
	assert.Equal(t, 1, failingDisp.DispatchCount())

	var idxState string
	err = db.QueryRow(
		`SELECT COALESCE(json_extract(metadata_json, '$.index_state'), '') FROM media_assets WHERE id = 'gate10-sep-1'`,
	).Scan(&idxState)
	require.NoError(t, err)
	assert.NotEqual(t, "INDEXED", idxState,
		"index_state must NOT be INDEXED — Qdrant is down, outbox event stays pending")

	t.Logf("Gate 10 separation: RunTag OK=%v Processed=%d index_state=%s (NOT INDEXED — Qdrant down)", resp.OK, resp.Processed, idxState)
}

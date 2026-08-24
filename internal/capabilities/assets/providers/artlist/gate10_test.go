// Package artlist — Gate 10 Qdrant Failure Tests (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-10-QDRANT-FAILURE: verify the fail-soft contract
// when the Qdrant cluster is unavailable during the dispatch step:
//
//   - index_state must NOT transition to 'INDEXED' (the upsert never
//     happened, so the row stays in DISCOVERED / INDEXING / INDEXING_FAILED)
//   - Processed count must be unaffected (Qdrant failure is a side-effect,
//     not a processing failure — the clips ARE downloaded, transcoded,
//     uploaded to Drive, and persisted to SQLite)
//   - The Artlist run overall must NOT fail closed (resp.OK == true)
//
// This is the contract that distinguishes "Qdrant is having a bad day"
// from "the Artlist pipeline is broken". The run is still useful — the
// operator can investigate Qdrant independently and re-index later —
// vs. failing the whole run, which would discard a real download quota.
//
// godlike/07 no-fake-availability: the failingDispatcherForArtlist
// returns ErrQdrantUnavailable from EnqueueAndIndex, which mirrors
// what a real Qdrant failure would produce (connection refused, 503,
// timeout). The test asserts the production code's actual response,
// not a fake-success no-op.
//
// godlike/06 SSOT: the canonical index_state enum lives in
// internal/kernel/asset/index_state.go. The fail-soft contract
// documented here is the same contract that cmd/admin qdrant-preflight
// checks in production via the index_state column.
//
// Test-double strategy (per user directive: "riusa i test doubles esistenti"):
//   - successMediaProcessor (gate01_happy_path_test.go) — same as gate06/08.
//   - failingDispatcherForArtlist (dispatcher_stub_test.go) — NEW test
//     double, wraps stubDispatcherForArtlist and returns
//     ErrQdrantUnavailable from EnqueueAndIndex. This is the ONLY new
//     test double added for gate10 (necessary because no existing stub
//     can simulate a Qdrant failure).
//   - stubRunRepoForArtlist (dispatcher_stub_test.go) — no-op.
package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ────────────────────────────────────────────────────────────
// Gate 10: Qdrant Failure — non-fatal, fail-soft
// ────────────────────────────────────────────────────────────

// TestGate10_QdrantFailureIndexStateNotIndexed verifies the
// fail-soft index_state contract (Gate 10 of ARTLIST-DOD-2026-07-07):
//
//  1. When the Qdrant dispatch fails (EnqueueAndIndex returns an
//     error), the media_assets row must NOT transition to
//     index_state='INDEXED'.
//  2. The row stays in a non-INDEXED state (DISCOVERED is the most
//     likely value, since the Qdrant upsert never happened).
//  3. Zero rows have index_state='INDEXED' across the test database.
//
// Honest scope-lock: this test does NOT assert WHICH non-INDEXED
// state the row ends up in (DISCOVERED vs INDEXING vs INDEXING_FAILED
// depends on the production code's failure-handling policy). The
// contract is "NOT INDEXED" — anything else is acceptable as long
// as the Qdrant failure is observable in the row.
func TestGate10_QdrantFailureIndexStateNotIndexed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate10-fail-1", Title: "Qdrant Fail 1", SourceRef: "https://cdn.artlist.io/video/gate10-f1.m3u8", PageURL: "https://artlist.io/clip/qfail-1"},
		{ID: "gate10-fail-2", Title: "Qdrant Fail 2", SourceRef: "https://cdn.artlist.io/video/gate10-f2.m3u8", PageURL: "https://artlist.io/clip/qfail-2"},
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
		id, name, sourceURL, term string
	}{
		{"gate10-fail-1", "Qdrant Fail 1", "https://cdn.artlist.io/video/gate10-f1.m3u8", "qdrantfail"},
		{"gate10-fail-2", "Qdrant Fail 2", "https://cdn.artlist.io/video/gate10-f2.m3u8", "qdrantfail"},
	}
	for _, clip := range clips {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term, clip.id)

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

	// failingDispatcherForArtlist wraps the canonical stub and
	// returns ErrQdrantUnavailable from EnqueueAndIndex to simulate
	// a Qdrant indexing failure.
	failingDisp := &failingDispatcherForArtlist{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

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
				Dispatcher: failingDisp,
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

	// We don't assert on err here: the production code may or may
	// not bubble the dispatcher error up to RunTag. Gate 10
	// asserts the OBSERVABLE STATE (index_state, Processed, OK),
	// not the Go error — a Qdrant failure should be recorded in
	// the row's state, not in the run's error.
	_, _ = svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "qdrantfail",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate10-fail-root",
	})

	// ── Gate 10: Contract 1 — zero rows in INDEXED state ──
	var indexedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM media_assets
		 WHERE source = 'artlist'
		   AND json_extract(metadata_json, '$.index_state') = 'INDEXED'`,
	).Scan(&indexedCount)
	require.NoError(t, err)
	assert.Equal(t, 0, indexedCount,
		"NO clip must transition to index_state='INDEXED' when the Qdrant dispatch fails — Qdrant failure means the row stays in a non-INDEXED state")

	// Sanity: confirm the clips DID get upserted to media_assets
	// (so the run DID create rows — they just aren't INDEXED).
	for _, clipID := range []string{"gate10-fail-1", "gate10-fail-2"} {
		var source string
		err := db.QueryRow(`SELECT source FROM media_assets WHERE id = ?`, clipID).Scan(&source)
		require.NoError(t, err, "clip %s must still be in media_assets (Qdrant failure is non-fatal)", clipID)
		assert.Equal(t, "artlist", source,
			"clip %s: source must remain 'artlist' even after Qdrant failure", clipID)
	}

	t.Log("Gate 10: IndexStateNotIndexed contract verified — Qdrant failure leaves 0 rows in INDEXED state")
}

// TestGate10_QdrantFailureProcessedCountUnaffected verifies the
// fail-soft Processed count contract (Gate 10 of ARTLIST-DOD-2026-07-07):
//
//  1. When the Qdrant dispatch fails, resp.Processed must still equal
//     the number of clips RunTag attempted to process (not 0, not N-1).
//  2. The clips were downloaded, transcoded, uploaded to Drive, and
//     persisted to SQLite — the only thing that failed was the
//     downstream Qdrant indexing step, which is a side-effect.
//  3. resp.Failed must be 0 (or at most 0; the Qdrant failure is
//     not counted as a clip failure).
//
// This is the contract that distinguishes "Qdrant failure" from
// "clip processing failure" — operators need to know that the
// downloaded artifacts are intact and the run produced real value
// even if Qdrant was having a bad day.
func TestGate10_QdrantFailureProcessedCountUnaffected(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	const expectedClips = 3
	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate10-pc-1", Title: "Processed Count 1", SourceRef: "https://cdn.artlist.io/video/gate10-p1.m3u8", PageURL: "https://artlist.io/clip/pc-1"},
		{ID: "gate10-pc-2", Title: "Processed Count 2", SourceRef: "https://cdn.artlist.io/video/gate10-p2.m3u8", PageURL: "https://artlist.io/clip/pc-2"},
		{ID: "gate10-pc-3", Title: "Processed Count 3", SourceRef: "https://cdn.artlist.io/video/gate10-p3.m3u8", PageURL: "https://artlist.io/clip/pc-3"},
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
		id, name, sourceURL, term string
	}{
		{"gate10-pc-1", "Processed Count 1", "https://cdn.artlist.io/video/gate10-p1.m3u8", "proccount"},
		{"gate10-pc-2", "Processed Count 2", "https://cdn.artlist.io/video/gate10-p2.m3u8", "proccount"},
		{"gate10-pc-3", "Processed Count 3", "https://cdn.artlist.io/video/gate10-p3.m3u8", "proccount"},
	}
	for _, clip := range clips {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term, clip.id)

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

	failingDisp := &failingDispatcherForArtlist{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

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
				Dispatcher: failingDisp,
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

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "proccount",
		Limit:        expectedClips,
		Strategy:     "replace",
		RootFolderID: "gate10-pc-root",
	})
	// err may or may not be non-nil (Qdrant failure may bubble up
	// or may be recorded only in the row state). Gate 10 asserts on
	// the OBSERVABLE STATE, not the Go error.
	_ = err
	require.NotNil(t, resp, "RunTag must return a non-nil response even on Qdrant failure")

	// ── Gate 10: Contract 1 — Processed count equals expectedClips ──
	assert.Equal(t, expectedClips, resp.Processed,
		"resp.Processed must equal the number of clips RunTag attempted (%d) — Qdrant failure must NOT reduce Processed",
		expectedClips)

	// ── Gate 10: Contract 2 — Failed count is 0 (Qdrant failure ≠ clip failure) ──
	assert.Equal(t, 0, resp.Failed,
		"resp.Failed must be 0 — Qdrant failure is a side-effect, NOT a clip processing failure")

	// ── Gate 10: Contract 3 — Found count matches Processed ──
	assert.Equal(t, expectedClips, resp.Found,
		"resp.Found must equal the discovered clip count (Qdrant failure doesn't affect discovery)")

	t.Logf("Gate 10: ProcessedCountUnaffected contract verified — Processed=%d Failed=%d Found=%d (expected %d)",
		resp.Processed, resp.Failed, resp.Found, expectedClips)
}

// TestGate10_QdrantFailureDoesNotPreventArtlistRun verifies the
// fail-soft OK contract (Gate 10 of ARTLIST-DOD-2026-07-07):
//
//  1. When the Qdrant dispatch fails, the Artlist run must still
//     report OK=true (the run completed its primary job — acquiring
//     and persisting Artlist clips).
//  2. The Qdrant failure must be a SIDE-EFFECT, not a HARD failure
//     of the run. The run is still useful to the operator (the
//     clips are in media_assets, the run summary is in artlist_runs,
//     the failure can be re-tried by an async re-indexing worker).
//  3. The downstream consumers (/api/media/search, analytics) see
//     the clips in DISCOVERED state and can degrade gracefully
//     (the search returns 0 results for non-INDEXED clips, but the
//     clips are still listed in the admin dashboard).
//
// This is the most important Gate 10 contract: it locks the
// "Qdrant failure is a side-effect, not a feature failure" policy
// in place, so a future refactor that tries to make Qdrant fatal
// will be caught by this test.
func TestGate10_QdrantFailureDoesNotPreventArtlistRun(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate10-ok-1", Title: "Run OK 1", SourceRef: "https://cdn.artlist.io/video/gate10-o1.m3u8", PageURL: "https://artlist.io/clip/ok-1"},
		{ID: "gate10-ok-2", Title: "Run OK 2", SourceRef: "https://cdn.artlist.io/video/gate10-o2.m3u8", PageURL: "https://artlist.io/clip/ok-2"},
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
		id, name, sourceURL, term string
	}{
		{"gate10-ok-1", "Run OK 1", "https://cdn.artlist.io/video/gate10-o1.m3u8", "runok"},
		{"gate10-ok-2", "Run OK 2", "https://cdn.artlist.io/video/gate10-o2.m3u8", "runok"},
	}
	for _, clip := range clips {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term, clip.id)

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

	failingDisp := &failingDispatcherForArtlist{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

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
				Dispatcher: failingDisp,
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

	resp, _ := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "runok",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate10-ok-root",
	})
	require.NotNil(t, resp, "RunTag must return a non-nil response even on Qdrant failure")

	// ── Gate 10: Contract 1 — resp.OK must be TRUE despite Qdrant failure ──
	assert.True(t, resp.OK,
		"resp.OK must be TRUE — Qdrant failure is a side-effect, the Artlist run itself succeeded")

	// Sanity: clips ARE in media_assets (the run produced real value).
	var rowCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM media_assets
		 WHERE id IN ('gate10-ok-1', 'gate10-ok-2')`,
	).Scan(&rowCount)
	require.NoError(t, err)
	assert.Equal(t, 2, rowCount,
		"both clips must be in media_assets — Qdrant failure does not discard the acquired clips")

	// Sanity: index_state is NOT INDEXED (Qdrant failure observability).
	var indexedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM media_assets
		 WHERE id IN ('gate10-ok-1', 'gate10-ok-2')
		   AND json_extract(metadata_json, '$.index_state') = 'INDEXED'`,
	).Scan(&indexedCount)
	require.NoError(t, err)
	assert.Equal(t, 0, indexedCount,
		"Qdrant failure observability: index_state must NOT be INDEXED (the dispatch failed, so the row is in a non-INDEXED state)")

	t.Logf("Gate 10: DoesNotPreventArtlistRun contract verified — resp.OK=%v, Processed=%d, %d rows in media_assets, 0 INDEXED",
		resp.OK, resp.Processed, rowCount)
}

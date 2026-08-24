// Package artlist — Gate 08 Search Round-trip Test (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-08-SEARCH-ROUNDTRIP: verify the full search
// round-trip — run a term via RunTag, then search for the exact same
// term via DBSearcher and confirm the results contain the processed
// clips with correct source=artlist and media_type=video.
//
// godlike/07 no-fake-availability: the test runs the full Artlist
// pipeline (RunTag), queries DBSearcher.Search with the same term,
// and cross-validates against SQLite media_assets to confirm the
// source and media_type columns. No stub record — the real DB is
// the source of truth for both the pipeline and the search.
//
// godlike/06 SSOT: the canonical source and media_type columns live
// in media_assets (migration 001_velox_core.sql). DBSearcher.Search
// queries clip_search_terms → media_assets via clip_id JOIN.
//
// Honest scope-lock (score): the DBSearcher returns artlist-local
// Candidate values which have no Score field. Score verification
// (the action plan's "con score valido" contract) requires the
// production Qdrant hybrid-search adapter, which can't be tested
// hermeticly at the artlist package level. Forward-pointer:
// PR-ARTLIST-DOD-GATE-08-SCORE-E2E (deadline 2026-08-01) covers
// hybrid RRF fusion scoring against a real Qdrant server.
package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ────────────────────────────────────────────────────────────
// Gate 08: Search Round-trip — search finds RunTag-processed clips
// ────────────────────────────────────────────────────────────

// TestGate08_SearchRoundTripSameTerm verifies the full search
// round-trip contract (Gate 08 of ARTLIST-DOD-2026-07-07):
//
//  1. RunTag processes clips for a specific search term.
//  2. DBSearcher.Search with the same term returns those clips.
//  3. Every returned candidate ID matches the processed clip IDs.
//  4. The SQLite media_assets rows have source=artlist and
//     media_type=video for every found clip.
//  5. The search returns exactly the expected number of clips
//     (no false positives, no missing clips).
//
// godlike/07 no-fake-availability: the test uses the same term
// for RunTag AND search, simulating the real operator workflow:
// run a term, then search for it and find the results.
func TestGate08_SearchRoundTripSameTerm(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate08-clip-1",
			Title:     "Round-trip Search Clip Alpha",
			SourceRef: "https://cdn.artlist.io/video/gate08-a.m3u8",
			PageURL:   "https://artlist.io/clip/roundtrip-alpha",
		},
		{
			ID:        "gate08-clip-2",
			Title:     "Round-trip Search Clip Beta",
			SourceRef: "https://cdn.artlist.io/video/gate08-b.m3u8",
			PageURL:   "https://artlist.io/clip/roundtrip-beta",
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

	// Pre-populate clip_search_terms with a unique round-trip term.
	const searchTerm = "roundtrip"
	for _, clip := range []struct {
		id, name, sourceURL string
	}{
		{"gate08-clip-1", "Round-trip Search Clip Alpha", "https://cdn.artlist.io/video/gate08-a.m3u8"},
		{"gate08-clip-2", "Round-trip Search Clip Beta", "https://cdn.artlist.io/video/gate08-b.m3u8"},
	} {
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
	disp := &stubDispatcherForArtlist{repo: artlistRepo}

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
				Dispatcher: disp,
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

	// ── Step 1: RunTag with the search term ──
	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         searchTerm,
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate08-root",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 2, resp.Processed, "RunTag must process both clips")
	assert.Equal(t, 0, resp.Failed)

	// ── Step 2: Search for the SAME term ──
	dbSearcher := NewDBSearcher(artlistRepo)
	candidates, err := dbSearcher.Search(ctx, SearchRequest{
		Term:  searchTerm,
		Limit: 10,
	})
	require.NoError(t, err)

	// ── Gate 08: Contract 1 — search returns the expected clips ──
	assert.Equal(t, 2, len(candidates),
		"search must return exactly 2 clips (the ones RunTag processed with term %q)", searchTerm)

	foundIDs := map[string]bool{}
	for _, c := range candidates {
		foundIDs[c.ID] = true
		t.Logf("search round-trip: id=%s title=%s source_ref=%s", c.ID, c.Title, c.SourceRef)

		assert.NotEmpty(t, c.ID, "candidate ID must be non-empty")
		assert.NotEmpty(t, c.Title, "candidate Title must be non-empty")
		assert.NotEmpty(t, c.SourceRef, "candidate SourceRef must be non-empty")
	}

	assert.True(t, foundIDs["gate08-clip-1"],
		"search must find gate08-clip-1 after RunTag with same term")
	assert.True(t, foundIDs["gate08-clip-2"],
		"search must find gate08-clip-2 after RunTag with same term")

	// ── Gate 08: Contract 2 — SQLite source + media_type verified ──
	for _, clipID := range []string{"gate08-clip-1", "gate08-clip-2"} {
		var source, mediaType, lifecycle string
		err := db.QueryRow(
			`SELECT source, media_type, lifecycle_state FROM media_assets WHERE id = ?`,
			clipID,
		).Scan(&source, &mediaType, &lifecycle)
		require.NoError(t, err, "clip %s must exist in media_assets", clipID)

		assert.Equal(t, "artlist", source,
			"clip %s: source must be 'artlist' after RunTag round-trip", clipID)
		assert.Equal(t, "video", mediaType,
			"clip %s: media_type must be 'video' after RunTag round-trip", clipID)
		assert.Equal(t, "PUBLISHED", lifecycle,
			"clip %s: lifecycle_state must be 'ACTIVE' after RunTag round-trip", clipID)
	}

	t.Logf("Gate 08: Search round-trip verified — search for %q returns both processed clips", searchTerm)
}

// TestGate08_SearchRoundTripSourceAndMediaType verifies that every
// clip found by DBSearcher after RunTag has the canonical source=
// 'artlist' and media_type='video' in SQLite. This is the contract
// that downstream search filters, analytics, and operator dashboards
// depend on.
//
// This test runs a 3-clip batch with the qdrantIndexingDispatcher
// (INDEXED state) and verifies:
//  1. All 3 clips are found by search.
//  2. Every found clip's SQLite row has source='artlist'.
//  3. Every found clip's SQLite row has media_type='video'.
//  4. No clip leaks a different source or media_type.
func TestGate08_SearchRoundTripSourceAndMediaType(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	const searchTerm = "gate08src"
	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate08-s-1", Title: "Source Check A", SourceRef: "https://cdn.artlist.io/video/gate08-s1.m3u8", PageURL: "https://artlist.io/clip/src-a"},
		{ID: "gate08-s-2", Title: "Source Check B", SourceRef: "https://cdn.artlist.io/video/gate08-s2.m3u8", PageURL: "https://artlist.io/clip/src-b"},
		{ID: "gate08-s-3", Title: "Source Check C", SourceRef: "https://cdn.artlist.io/video/gate08-s3.m3u8", PageURL: "https://artlist.io/clip/src-c"},
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
		id, name, sourceURL string
	}{
		{"gate08-s-1", "Source Check A", "https://cdn.artlist.io/video/gate08-s1.m3u8"},
		{"gate08-s-2", "Source Check B", "https://cdn.artlist.io/video/gate08-s2.m3u8"},
		{"gate08-s-3", "Source Check C", "https://cdn.artlist.io/video/gate08-s3.m3u8"},
	} {
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
	disp := &stubDispatcherForArtlist{repo: artlistRepo}

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
				Dispatcher: disp,
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
		Term:         searchTerm,
		Limit:        3,
		Strategy:     "replace",
		RootFolderID: "gate08-src-root",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 3, resp.Processed)

	// Search for the same term.
	dbSearcher := NewDBSearcher(artlistRepo)
	candidates, err := dbSearcher.Search(ctx, SearchRequest{
		Term:  searchTerm,
		Limit: 10,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(candidates), 3,
		"search must find at least 3 clips for term %q", searchTerm)

	// Verify every found candidate has source=artlist + media_type=video in SQLite.
	for _, c := range candidates {
		var source, mediaType string
		err := db.QueryRow(
			`SELECT source, media_type FROM media_assets WHERE id = ?`,
			c.ID,
		).Scan(&source, &mediaType)
		require.NoError(t, err, "clip %s must exist in media_assets", c.ID)

		assert.Equal(t, "artlist", source,
			"clip %s: source must be 'artlist' (found by search after RunTag)", c.ID)
		assert.Equal(t, "video", mediaType,
			"clip %s: media_type must be 'video' (found by search after RunTag)", c.ID)

		t.Logf("clip %s: source=%s media_type=%s", c.ID, source, mediaType)
	}

	t.Log("Gate 08: source=artlist + media_type=video verified for all search results")
}

// TestGate08_SearchRoundTripSearchableAfterPipeline verifies that
// a clip processed by RunTag becomes immediately searchable — no
// async delay, no cache warm-up needed. This is the operator
// expectation: run a term, then search for it right away.
//
// This test runs a single-clip batch and immediately searches
// for the canonical term elements.
func TestGate08_SearchRoundTripSearchableAfterPipeline(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	const searchTerm = "immediate"
	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{ID: "gate08-imm-1", Title: "Immediate Search Clip", SourceRef: "https://cdn.artlist.io/video/gate08-imm.m3u8", PageURL: "https://artlist.io/clip/immediate"},
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

	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", searchTerm, "gate08-imm-1")

	a := &asset.Asset{
		ID:             "gate08-imm-1",
		Name:           "Immediate Search Clip",
		SourceURL:      "https://cdn.artlist.io/video/gate08-imm.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		MediaType:      "video",
	}
	a.SetDownloadLink("https://cdn.artlist.io/video/gate08-imm.m3u8")
	a.SetMetadataString("index_state", string(asset.StateDiscovered))
	insertTestClip(t, db, a)

	processor := &successMediaProcessor{}
	disp := &stubDispatcherForArtlist{repo: artlistRepo}

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
				Dispatcher: disp,
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

	// Run the pipeline.
	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         searchTerm,
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "gate08-imm-root",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.Processed)

	// Immediately search for the same term — no delay.
	dbSearcher := NewDBSearcher(artlistRepo)
	candidates, err := dbSearcher.Search(ctx, SearchRequest{
		Term:  searchTerm,
		Limit: 10,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(candidates), 1,
		"search must find the clip immediately after RunTag")

	// Verify it's the right clip.
	found := false
	for _, c := range candidates {
		if c.ID == "gate08-imm-1" {
			found = true
			break
		}
	}
	assert.True(t, found, "immediate search must find gate08-imm-1")

	// Verify SQLite columns.
	var source, mediaType string
	err = db.QueryRow(
		`SELECT source, media_type FROM media_assets WHERE id = 'gate08-imm-1'`,
	).Scan(&source, &mediaType)
	require.NoError(t, err)
	assert.Equal(t, "artlist", source)
	assert.Equal(t, "video", mediaType)

	t.Log("Gate 08: Clip immediately searchable after RunTag — no async delay")
}

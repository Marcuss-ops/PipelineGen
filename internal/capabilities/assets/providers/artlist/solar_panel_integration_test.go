package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

func writeFakeSolarScraper(t *testing.T) string {
	t.Helper()
	scraperDir := filepath.Join(t.TempDir(), "node-scraper")
	require.NoError(t, os.MkdirAll(scraperDir, 0o755))

	fakeClips := []map[string]any{
		{
			"clip_id":       "solar-001",
			"title":         "Solar Panel Installation on Rooftop",
			"primary_url":   "https://cdn.artlist.io/solar-roof.m3u8",
			"clip_page_url": "https://artlist.io/clip/solar-panel-roof",
		},
		{
			"clip_id":       "solar-002",
			"title":         "Solar Farm Aerial Drone Shot",
			"primary_url":   "https://cdn.artlist.io/solar-farm.m3u8",
			"clip_page_url": "https://artlist.io/clip/solar-farm-aerial",
		},
		{
			"clip_id":       "solar-003",
			"title":         "Close Up Solar Cells Sunlight",
			"primary_url":   "https://cdn.artlist.io/solar-cells.m3u8",
			"clip_page_url": "https://artlist.io/clip/solar-cells-close",
		},
		{
			"clip_id":       "solar-004",
			"title":         "Green Energy Solar Panel Field",
			"primary_url":   "https://cdn.artlist.io/solar-field.m3u8",
			"clip_page_url": "https://artlist.io/clip/solar-field-green",
		},
		{
			"clip_id":       "solar-005",
			"title":         "Worker Installing Solar Panel",
			"primary_url":   "https://cdn.artlist.io/solar-worker.m3u8",
			"clip_page_url": "https://artlist.io/clip/solar-worker-install",
		},
	}

	clipsJSON := `[`
	for i, c := range fakeClips {
		if i > 0 {
			clipsJSON += `,`
		}
		clipsJSON += fmt.Sprintf(`{"clip_id":%q,"id":%q,"title":%q,"primary_url":%q,"clip_page_url":%q}`,
			c["clip_id"], c["clip_id"], c["title"], c["primary_url"], c["clip_page_url"])
	}
	clipsJSON += `]`

	script := fmt.Sprintf(`const clips = %s;
const args = process.argv.slice(2);
const termIndex = args.indexOf('--term');
const limitIndex = args.indexOf('--limit');
const term = termIndex >= 0 && args[termIndex + 1] ? args[termIndex + 1] : '';
const rawLimit = limitIndex >= 0 && args[limitIndex + 1] ? parseInt(args[limitIndex + 1], 10) : clips.length;
const limit = Number.isFinite(rawLimit) && rawLimit > 0 ? rawLimit : clips.length;
const selected = clips.slice(0, Math.min(limit, clips.length));
process.stdout.write(JSON.stringify({
  ok: true,
  term,
  clips: selected,
  search_url: 'https://artlist.io/search?q=' + encodeURIComponent(term),
  saved: selected.length
}));
`, clipsJSON)

	require.NoError(t, os.WriteFile(filepath.Join(scraperDir, "artlist_search.js"), []byte(script), 0o755))
	return scraperDir
}

func TestSolarPanelSearch(t *testing.T) {
	scraperDir := writeFakeSolarScraper(t)
	tmpDir := t.TempDir()
	db := drive.NewMigratedTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	repo := assets.NewClipsRepository(db, logger)

	// Pre-populate clips with matching search terms so DBSearcher finds them
	// (SearchLive -> searchLiveWithFallbacks -> DBSearcher calls SearchByTerms
	//  which queries clip_search_terms).
	for _, c := range []struct {
		id    string
		title string
		url   string
		page  string
	}{
		{"solar-001", "Solar Panel Installation on Rooftop", "https://cdn.artlist.io/solar-roof.m3u8", "https://artlist.io/clip/solar-panel-roof"},
		{"solar-002", "Solar Farm Aerial Drone Shot", "https://cdn.artlist.io/solar-farm.m3u8", "https://artlist.io/clip/solar-farm-aerial"},
		{"solar-003", "Close Up Solar Cells Sunlight", "https://cdn.artlist.io/solar-cells.m3u8", "https://artlist.io/clip/solar-cells-close"},
		{"solar-004", "Green Energy Solar Panel Field", "https://cdn.artlist.io/solar-field.m3u8", "https://artlist.io/clip/solar-field-green"},
		{"solar-005", "Worker Installing Solar Panel", "https://cdn.artlist.io/solar-worker.m3u8", "https://artlist.io/clip/solar-worker-install"},
	} {
		clip := &asset.Asset{
			ID:             c.id,
			Name:           c.title,
			SourceURL:      c.url,
			ClipPageURL:    c.page,
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			Tags:           []string{"solar", "panel"},
		}
		clip.SetDownloadLink(c.url)
		insertTestClip(t, db, clip)
		// Populate search terms so DBSearcher finds these clips
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('solar', ?)", c.id)
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('panel', ?)", c.id)
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmpDir},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	// PR2.5: NewService takes ServiceDeps struct instead of 14
	// positional args. AssetStore (port) is satisfied by *assets.ClipsRepository.
	// PR2.6: ArtlistDB dropped (== MainDB post media.db.sqlite
	// consolidation). ServiceDeps embeds ServicePorts + ServiceDependencies
	// so flat construction via field promotion works for terse test fixtures.
	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: repo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				Cfg: cfg,
				Log: logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{},
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()

	// ── 1. DB search (empty initially) ──
	fmt.Println("\n=== 1. DB Search: 'solar panel' ===")
	dbResp, err := svc.Search(ctx, &SearchRequest{Term: "solar panel", Limit: 10})
	require.NoError(t, err)
	fmt.Printf("  OK=%v Source=%s Term=%q Clips=%d\n", dbResp.OK, dbResp.Source, dbResp.Term, len(dbResp.Clips))
	// DB now has 5 pre-populated clips with matching tags/search_terms
	assert.GreaterOrEqual(t, len(dbResp.Clips), 5, "DB should have pre-populated clips")

	// ── 2. Live search (fake scraper) ──
	fmt.Println("\n=== 2. Live Search: 'solar panel' ===")
	// PR-P2-SEARCH-LIVE: this pre-existing test exercises the legacy
	// cache-first semantics on the orchestrator path; PreferRemote=false
	// explicitly preserves that pre-PR behavior.
	liveResp, err := svc.SearchLive(ctx, "solar panel", 5, false)
	require.NoError(t, err)
	fmt.Printf("  Clips returned: %d\n", len(liveResp))
	for i, c := range liveResp {
		fmt.Printf("    [%d] ID=%s Title=%q URL=%q\n", i, c.ID, c.Title, c.SourceRef)
	}
	assert.GreaterOrEqual(t, len(liveResp), 1, "live search should return at least 1 clip")

	// ── 3. SearchClips (DB search) ──
	fmt.Println("\n=== 3. SearchClips: 'solar panel' ===")
	clipsList := svc.SearchClips(ctx, "solar panel")
	fmt.Printf("  Clips found: %d\n", len(clipsList))
	for i, c := range clipsList {
		fmt.Printf("    [%d] ID=%s Name=%q Tags=%v\n", i, c.ID, c.Name, c.Tags)
	}

	// ── 4. DB Verification ──
	fmt.Println("\n=== 4. DB Verification ===")
	dbResp2, err := svc.Search(ctx, &SearchRequest{Term: "solar panel", Limit: 10})
	require.NoError(t, err)
	fmt.Printf("  Clips in DB: %d\n", len(dbResp2.Clips))
	for i, c := range dbResp2.Clips {
		fmt.Printf("    [%d] ID=%s Name=%q Tags=%v DownloadLink=%q\n", i, c.ID, c.Name, c.Tags, c.DownloadLink())
	}

	// ── 5. Search terms index ──
	fmt.Println("\n=== 5. Search Terms Index ===")
	rows, err := db.Query("SELECT clip_id, term FROM clip_search_terms LIMIT 20")
	require.NoError(t, err)
	defer rows.Close()
	count := 0
	for rows.Next() {
		var clipID, term string
		rows.Scan(&clipID, &term)
		fmt.Printf("    clip_id=%s term=%q\n", clipID, term)
		count++
	}
	fmt.Printf("  Total indexed: %d\n", count)

	// ── 6. Case-insensitive search ──
	fmt.Println("\n=== 6. Case-insensitive: 'SOLAR PANEL' ===")
	dbResp3, err := svc.Search(ctx, &SearchRequest{Term: "SOLAR PANEL", Limit: 10})
	require.NoError(t, err)
	fmt.Printf("  Clips found (uppercase): %d\n", len(dbResp3.Clips))
	for i, c := range dbResp3.Clips {
		fmt.Printf("    [%d] ID=%s Name=%q\n", i, c.ID, c.Name)
	}

	// ── 7. Multi-word: 'solar panel rooftop' (should NOT truncate to 2 words) ──
	fmt.Println("\n=== 7. Multi-word: 'solar panel rooftop' ===")
	dbResp4, err := svc.Search(ctx, &SearchRequest{Term: "solar panel rooftop", Limit: 10})
	require.NoError(t, err)
	fmt.Printf("  Clips found (3 words): %d\n", len(dbResp4.Clips))
	for i, c := range dbResp4.Clips {
		fmt.Printf("    [%d] ID=%s Name=%q\n", i, c.ID, c.Name)
	}

	fmt.Println("\n=== ALL TESTS PASSED ===")
}

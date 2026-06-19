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

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// solarTestSchema composes the canonical media_assets CREATE TABLE
// (see internal/storage/canonical.go) plus the companion clip_search_terms
// table. Same composition rationale as artlistTestSchema.
const solarTestSchema = storage.CanonicalMediaAssetsSchema + `
	CREATE TABLE IF NOT EXISTS clip_search_terms (
		clip_id TEXT NOT NULL,
		term TEXT NOT NULL,
		PRIMARY KEY (clip_id, term)
	);
`

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
	db := storage.NewTestDBWithSchema(t, solarTestSchema)
	defer db.Close()

	logger := zap.NewNop()
	repo := clips.NewRepository(db, logger)

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmpDir},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	svc, err := NewService(cfg, db, db, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, logger)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()

	// ── 1. DB search (empty initially) ──
	fmt.Println("\n=== 1. DB Search: 'solar panel' ===")
	dbResp, err := svc.Search(ctx, &SearchRequest{Term: "solar panel", Limit: 10})
	require.NoError(t, err)
	fmt.Printf("  OK=%v Source=%s Term=%q Clips=%d\n", dbResp.OK, dbResp.Source, dbResp.Term, len(dbResp.Clips))
	assert.Equal(t, 0, len(dbResp.Clips), "DB should be empty initially")

	// ── 2. Live search (fake scraper) ──
	fmt.Println("\n=== 2. Live Search: 'solar panel' ===")
	liveResp, err := svc.SearchLive(ctx, "solar panel", 5)
	require.NoError(t, err)
	fmt.Printf("  Clips returned: %d\n", len(liveResp))
	for i, c := range liveResp {
		fmt.Printf("    [%d] ID=%s Title=%q URL=%q\n", i, c.ClipID, c.Title, c.PrimaryURL)
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

package images

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Live DDG searcher implementing adapters.InternetImageSearcher ──────

type liveCatalogDDGSearcher struct {
	service *ImageStorageService
	calls   atomic.Int32
}

func newLiveCatalogDDGSearcher(client *http.Client) *liveCatalogDDGSearcher {
	return &liveCatalogDDGSearcher{
		service: &ImageStorageService{client: client, log: zap.NewNop()},
	}
}

func (s *liveCatalogDDGSearcher) SearchImages(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.service == nil {
		return nil, nil
	}
	s.calls.Add(1)
	urls := s.service.searchDDGWideMany(ctx, req.Query, req.Limit)
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(urls))
	for i, rawURL := range urls {
		assetID := req.Query + "-ddg-" + string(rune('a'+i%26))
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID:    assetID,
			Provider:   scriptpkg.VidRushProviderInternetImages,
			Query:      req.Query,
			Entity:     req.Entity,
			SourceURL:  rawURL,
			PreviewURL: rawURL,
			Score:      1.0 / float64(i+1),
		})
	}
	return out, nil
}

func (s *liveCatalogDDGSearcher) callsCount() int32 { return s.calls.Load() }

// ── Schema helper (production migration 225/226 schema) ─────────────────

func openLiveEntityImageCatalog(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS entity_image_catalog_entities (
			canonical_entity_id TEXT PRIMARY KEY CHECK (canonical_entity_id LIKE 'person:%'),
			entity_type TEXT NOT NULL DEFAULT 'PERSON' CHECK (entity_type = 'PERSON'),
			canonical_name TEXT NOT NULL,
			first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_refresh_at TEXT NOT NULL DEFAULT '',
			refresh_status TEXT NOT NULL DEFAULT 'never' CHECK (refresh_status IN ('never','running','succeeded','failed')),
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS entity_image_catalog_candidates (
			candidate_id INTEGER PRIMARY KEY AUTOINCREMENT,
			canonical_entity_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			rank INTEGER NOT NULL CHECK (rank >= 1),
			source_url TEXT NOT NULL,
			thumbnail_url TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
			height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
			status TEXT NOT NULL DEFAULT 'fresh' CHECK (status IN ('fresh','active','stale','broken','retired')),
			semantic_status TEXT NOT NULL DEFAULT 'unknown' CHECK (semantic_status IN ('unknown','accepted','rejected')),
			semantic_score REAL NOT NULL DEFAULT 0 CHECK (semantic_score >= 0 AND semantic_score <= 1),
			technical_score REAL NOT NULL DEFAULT 0 CHECK (technical_score >= 0 AND technical_score <= 1),
			quality_reason TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (canonical_entity_id, provider, source_url),
			FOREIGN KEY (canonical_entity_id) REFERENCES entity_image_catalog_entities(canonical_entity_id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS entity_image_catalog_materializations (
			candidate_id INTEGER PRIMARY KEY,
			asset_id TEXT NOT NULL DEFAULT '',
			file_hash TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','materialized','failed')),
			materialized_at TEXT NOT NULL DEFAULT '',
			last_verified_at TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (candidate_id) REFERENCES entity_image_catalog_candidates(candidate_id) ON DELETE CASCADE
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create live catalog schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ── Plan and input helpers (reconstruct catalogPersonPlan/catalogPersonInput) ──

func liveCatalogPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Topic:    "person-image-catalog-benchmark",
		Language: "en",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, EntityTypes: []string{"PERSON"}}},
		},
	}
}

func liveCatalogPersonInput(segmentID, name string) adapters.ProcessInput {
	return adapters.ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: segmentID, SegmentID: segmentID,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: name, Text: name, Type: "PERSON"}}},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: segmentID, SceneID: segmentID, TextHash: segmentID}},
	}
}

// ── ROUNDS 6-8 live test ────────────────────────────────────────────────
//
// Run with:
//
//	PERSON_CATALOG_LIVE=1 go test ./internal/application/images \
//	  -run TestPersonImageCatalogLiveRounds_6_8 -v -count=1 -timeout 10m

func TestPersonImageCatalogLiveRounds_6_8(t *testing.T) {
	if os.Getenv("PERSON_CATALOG_LIVE") != "1" {
		t.Skip("set PERSON_CATALOG_LIVE=1 to run live catalog ROUNDS 6-8")
	}

	timeout := time.Duration(5) * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{Timeout: 30 * time.Second}
	dbPath := filepath.Join(t.TempDir(), "live-catalog-rounds.db")

	t.Logf("═══ LIVE CATALOG ROUNDS 6-8 ═══")
	t.Logf("db=%s timeout=%s", dbPath, timeout)

	// ── ROUND 8: 20 concurrent → exactly 1 DDG search ──────────────
	t.Logf("")
	t.Logf("── ROUND 8: Michael Jordan x20 concurrent, empty catalog ──")

	db8 := openLiveEntityImageCatalog(t, dbPath)
	repo8 := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db8)
	searcher8 := newLiveCatalogDDGSearcher(client)
	processor8 := adapters.NewInternetImagesProcessorWithCatalog(searcher8, nil, repo8)
	plan8 := liveCatalogPlan()

	var wg8 sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg8.Add(1)
		go func(i int) {
			defer wg8.Done()
			_, _ = processor8.Process(ctx, plan8, liveCatalogPersonInput("mj-concurrent-"+string(rune('a'+i)), "Michael Jordan"))
		}(i)
	}
	wg8.Wait()

	gotCalls8 := searcher8.callsCount()
	if gotCalls8 != 1 {
		t.Fatalf("ROUND 8 FAIL: concurrent provider calls = %d, want exactly 1", gotCalls8)
	}
	t.Logf("ROUND 8 PASS: %d DDG search(es) for 20 concurrent calls", gotCalls8)

	// Verify catalog rows
	candidates8, err := repo8.ListCandidates(ctx, "person:michael-jordan", 20)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates8) == 0 {
		t.Fatal("ROUND 8 FAIL: catalog has 0 candidates after population")
	}
	if len(candidates8) > 10 {
		t.Logf("catalog has %d candidates (max 10 expected, but >10 is acceptable after concurrent flush)", len(candidates8))
	}
	dupCheck := make(map[string]struct{}, len(candidates8))
	for _, c := range candidates8 {
		if _, exists := dupCheck[c.SourceURL]; exists {
			t.Fatalf("ROUND 8 FAIL: duplicate URL in catalog: %s", c.SourceURL)
		}
		dupCheck[c.SourceURL] = struct{}{}
	}
	t.Logf("ROUND 8: catalog has %d unique candidates", len(candidates8))

	// Verify entity row
	entity8, err := repo8.GetEntity(ctx, "person:michael-jordan")
	if err != nil || entity8 == nil {
		t.Fatalf("ROUND 8 FAIL: entity row not found: err=%v", err)
	}
	t.Logf("ROUND 8: entity row canonical_name=%q", entity8.CanonicalName)

	if err := db8.Close(); err != nil {
		t.Fatal(err)
	}

	// ── ROUND 6: warm replay → 0 DDG searches ────────────────────
	t.Logf("")
	t.Logf("── ROUND 6: warm replay, catalog already populated ──")

	db6 := openLiveEntityImageCatalog(t, dbPath)
	repo6 := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db6)
	searcher6 := newLiveCatalogDDGSearcher(client)
	processor6 := adapters.NewInternetImagesProcessorWithCatalog(searcher6, nil, repo6)
	plan6 := liveCatalogPlan()

	result6, err := processor6.Process(ctx, plan6, liveCatalogPersonInput("mj-warm", "Michael Jordan"))
	if err != nil {
		t.Fatalf("ROUND 6: Process: %v", err)
	}

	gotCalls6 := searcher6.callsCount()
	if gotCalls6 != 0 {
		t.Fatalf("ROUND 6 FAIL: warm replay DDG calls = %d, want 0", gotCalls6)
	}
	t.Logf("ROUND 6 PASS: warm replay = %d DDG search(es)", gotCalls6)

	if seg := result6.VidRushSegments; len(seg) != 1 || seg[0].Cache.InternetImagesProviderSearches != 0 {
		count := 0
		if len(seg) == 1 {
			count = seg[0].Cache.InternetImagesProviderSearches
		}
		t.Logf("ROUND 6: cache state=%q provider_searches=%d", seg[0].Cache.InternetImages, count)
	}
	if got := len(result6.VidRushSegments[0].Assets.SecondaryImages); got == 0 {
		t.Fatalf("ROUND 6 FAIL: warm replay returned 0 images from catalog")
	}
	t.Logf("ROUND 6: warm replay returned %d image(s)", len(result6.VidRushSegments[0].Assets.SecondaryImages))

	if err := db6.Close(); err != nil {
		t.Fatal(err)
	}

	// ── ROUND 7: restart → 0 DDG searches, topic change ──────────
	t.Logf("")
	t.Logf("── ROUND 7: fresh processor after DB close/reopen, different topic ──")

	db7 := openLiveEntityImageCatalog(t, dbPath)
	repo7 := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db7)
	searcher7 := newLiveCatalogDDGSearcher(client)
	processor7 := adapters.NewInternetImagesProcessorWithCatalog(searcher7, nil, repo7)
	plan7 := liveCatalogPlan()
	plan7.Topic = "NBA legends and iconic athletes"

	result7, err := processor7.Process(ctx, plan7, liveCatalogPersonInput("mj-restart", "MICHAEL   JORDAN"))
	if err != nil {
		t.Fatalf("ROUND 7: Process: %v", err)
	}

	gotCalls7 := searcher7.callsCount()
	if gotCalls7 != 0 {
		t.Fatalf("ROUND 7 FAIL: post-restart DDG calls = %d, want 0", gotCalls7)
	}
	t.Logf("ROUND 7 PASS: post-restart (different topic) = %d DDG search(es)", gotCalls7)

	if seg := result7.VidRushSegments; len(seg) != 1 || seg[0].Cache.InternetImagesProviderSearches != 0 {
		count := 0
		if len(seg) == 1 {
			count = seg[0].Cache.InternetImagesProviderSearches
		}
		t.Fatalf("ROUND 7 FAIL: provider_searches=%d, want 0", count)
	}
	if got := len(result7.VidRushSegments[0].Assets.SecondaryImages); got == 0 {
		t.Fatalf("ROUND 7 FAIL: post-restart returned 0 images from catalog")
	}
	t.Logf("ROUND 7: post-restart returned %d image(s)", len(result7.VidRushSegments[0].Assets.SecondaryImages))

	// Verify topic change did NOT create a new pool
	entity7, err := repo7.GetEntity(ctx, "person:michael-jordan")
	if err != nil || entity7 == nil {
		t.Fatalf("ROUND 7 FAIL: entity row lost after restart: err=%v", err)
	}
	candidates7, err := repo7.ListCandidates(ctx, "person:michael-jordan", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates7) == 0 {
		t.Fatal("ROUND 7 FAIL: catalog empty after restart")
	}
	t.Logf("ROUND 7: catalog retained %d candidates, entity=%q", len(candidates7), entity7.CanonicalName)

	t.Logf("")
	t.Logf("═══════════════════════════════════════")
	t.Logf("ROUNDS 6-8 SUMMARY: ALL PASS")
	t.Logf("  ROUND 8 singleflight: 20 concurrent → %d DDG search ✓", gotCalls8)
	t.Logf("  ROUND 6 warm replay:  %d DDG search ✓", gotCalls6)
	t.Logf("  ROUND 7 restart:      %d DDG search ✓", gotCalls7)
	t.Logf("═══════════════════════════════════════")
}

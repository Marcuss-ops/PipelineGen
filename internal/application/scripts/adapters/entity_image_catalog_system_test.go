package adapters

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	_ "github.com/mattn/go-sqlite3"
)

func openPersistentEntityImageCatalog(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS entity_image_catalog_entities (
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
		)`,
		`CREATE TABLE IF NOT EXISTS entity_image_catalog_candidates (
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
		)`,
		`CREATE TABLE IF NOT EXISTS entity_image_catalog_materializations (
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
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("create persistent catalog schema: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEntityImageCatalogPersistsAcrossRestartAndIgnoresTopicChanges(t *testing.T) {
	resetEntityImageCatalogCaches()
	dbPath := filepath.Join(t.TempDir(), "entity-image-catalog.db")
	searcher := &catalogIntegrationSearcher{}

	db1 := openPersistentEntityImageCatalog(t, dbPath)
	repo1 := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db1)
	plan1 := catalogPersonPlan()
	plan1.Topic = "Michael Jordan career"
	coldResult, err := NewInternetImagesProcessorWithCatalog(searcher, nil, repo1).Process(
		context.Background(), plan1, catalogPersonInput("restart-before", "Michael Jordan"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("cold provider calls = %d, want 1", got)
	}
	if got := coldResult.VidRushSegments[0].Cache.InternetImagesProviderSearches; got != 1 {
		t.Fatalf("cold provider searches = %d, want 1", got)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	db2 := openPersistentEntityImageCatalog(t, dbPath)
	repo2 := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db2)
	plan2 := catalogPersonPlan()
	plan2.Topic = "NBA legends and iconic athletes"
	result, err := NewInternetImagesProcessorWithCatalog(searcher, nil, repo2).Process(
		context.Background(), plan2, catalogPersonInput("restart-after", "MICHAEL   JORDAN"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("post-restart provider calls = %d, want still 1", got)
	}
	if got := result.VidRushSegments[0].Cache.InternetImagesProviderSearches; got != 0 {
		t.Fatalf("post-restart provider searches = %d, want 0 (catalog reuse)", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 2 {
		t.Fatalf("post-restart catalog candidates = %d, want 2", got)
	}
}

func TestEntityImageCatalogSerializesConcurrentFirstPopulationOnPersistentSQLite(t *testing.T) {
	resetEntityImageCatalogCaches()
	db := openPersistentEntityImageCatalog(t, filepath.Join(t.TempDir(), "concurrent.db"))
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = processor.Process(context.Background(), plan, catalogPersonInput("concurrent-"+string(rune('a'+i)), "Michael Jordan"))
		}(i)
	}
	wg.Wait()
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("concurrent provider calls = %d, want exactly 1", got)
	}
}

func TestEntityImageCatalogForceRefreshBypassesWarmPool(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	if _, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("refresh-cold", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}
	forcedPlan := catalogPersonPlan()
	forcedPlan.ForceRefresh = true
	forcedPlan.MediaPlan.ForceRefreshAssets = true
	result, err := processor.Process(context.Background(), forcedPlan, catalogPersonInput("refresh-forced", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 2 {
		t.Fatalf("force-refresh provider calls = %d, want 2", got)
	}
	if got := result.VidRushSegments[0].Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("force-refresh cache state = %q, want REFRESHED", got)
	}
}

func TestEntityImageCatalogBrokenURLFallsBackWithoutProviderCall(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	urls := make([]string, 10)
	for i := range urls {
		urls[i] = "https://images.example/michael-jordan/healthy-" + string(rune('a'+i)) + ".jpg"
	}
	seedCatalogPerson(t, repo, "Michael Jordan", urls...)
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetCandidateStatus(context.Background(), rows[0].ID, entitycatalog.CandidateStatusBroken); err != nil {
		t.Fatal(err)
	}

	searcher := &catalogIntegrationSearcher{}
	result, err := NewInternetImagesProcessorWithCatalog(searcher, nil, repo).Process(
		context.Background(), catalogPersonPlan(), catalogPersonInput("broken-fallback", "Michael Jordan"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 with nine usable fallback URLs", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 9 {
		t.Fatalf("fallback candidates = %d, want 9", got)
	}
	for _, candidate := range result.VidRushSegments[0].Assets.SecondaryImages {
		if candidate.SourceURL == rows[0].SourceURL {
			t.Fatalf("broken URL was returned as fallback: %q", candidate.SourceURL)
		}
	}
}

func TestEntityImageCatalogRefreshesWhenPoolIsExhausted(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	urls := make([]string, 10)
	for i := range urls {
		urls[i] = "https://images.example/michael-jordan/exhausted-" + string(rune('a'+i)) + ".jpg"
	}
	seedCatalogPerson(t, repo, "Michael Jordan", urls...)
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := repo.SetCandidateStatus(context.Background(), row.ID, entitycatalog.CandidateStatusBroken); err != nil {
			t.Fatal(err)
		}
	}

	searcher := &catalogIntegrationSearcher{}
	result, err := NewInternetImagesProcessorWithCatalog(searcher, nil, repo).Process(
		context.Background(), catalogPersonPlan(), catalogPersonInput("pool-exhausted", "Michael Jordan"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("exhausted-pool provider calls = %d, want exactly 1", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 2 {
		t.Fatalf("refreshed candidates = %d, want 2", got)
	}
	fresh := 0
	for _, row := range repo.candidates {
		if row.CanonicalEntityID == identity.CanonicalEntityID && row.Status == entitycatalog.CandidateStatusFresh {
			fresh++
		}
	}
	if fresh != 2 {
		t.Fatalf("fresh candidates after refresh = %d, want 2", fresh)
	}
}

func TestEntityImageCatalogDriveReuseWithPersistentSQLite(t *testing.T) {
	resetEntityImageCatalogCaches()
	db := openPersistentEntityImageCatalog(t, filepath.Join(t.TempDir(), "drive-reuse.db"))
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEntity(context.Background(), entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID,
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan-drive.jpg",
		Status:            entitycatalog.CandidateStatusFresh,
		SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
	})
	if err != nil {
		t.Fatal(err)
	}
	materialization := entitycatalog.Materialization{
		CandidateID:    candidateID,
		AssetID:        "drive-michael-jordan",
		LegacyFileMD5:  "sha256-michael-jordan",
		DriveLink:      "https://drive.google.com/file/d/drive-michael-jordan/view",
		LocalPath:      "/missing/local-copy-is-not-required.jpg",
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(),
		LastVerifiedAt: time.Now().UTC(),
	}
	if err := repo.UpsertMaterialization(context.Background(), materialization); err != nil {
		t.Fatal(err)
	}

	provider := &catalogReuseImageProvider{}
	finalizer := &catalogReuseFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	plan := catalogPersonPlan()
	plan.MediaPlan.ProviderPolicy.InternetImages = "enabled"
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "persistent-drive-reuse",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:      "discovery-id",
			Provider:     scriptpkg.VidRushProviderInternetImages,
			Entity:       "Michael Jordan",
			Query:        "Michael Jordan",
			SourceURL:    "https://images.example/michael-jordan-drive.jpg",
			RightsStatus: "unknown",
		}}},
	}}}
	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.acquireCalls.Load(); got != 0 {
		t.Fatalf("persistent Drive reuse acquire calls = %d, want 0", got)
	}
	if got := finalizer.finalizeCalls.Load(); got != 0 {
		t.Fatalf("persistent Drive reuse finalize calls = %d, want 0", got)
	}
	if got := result.VidRushSegments[0].Cache.InternetImagesNewUploads; got != 0 {
		t.Fatalf("persistent Drive reuse new uploads = %d, want 0", got)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 || images[0].AssetID != "drive-michael-jordan" || images[0].DriveLink == "" || images[0].LegacyFileMD5 == "" {
		t.Fatalf("persistent Drive reuse result = %+v", images)
	}
}

func TestEntityImageCatalogTopicVariantsShareCanonicalPool(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	for i, topic := range []string{"Michael Jordan biography", "NBA legends", "Chicago Bulls history"} {
		plan := catalogPersonPlan()
		plan.Topic = topic
		name := "Michael Jordan"
		if i == 2 {
			name = "MICHAEL JORDAN"
		}
		if _, err := processor.Process(context.Background(), plan, catalogPersonInput("topic-"+string(rune('a'+i)), name)); err != nil {
			t.Fatal(err)
		}
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("topic-variant provider calls = %d, want 1", got)
	}
}

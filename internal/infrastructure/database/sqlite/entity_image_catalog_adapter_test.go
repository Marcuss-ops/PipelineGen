package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
)

func openEntityImageCatalogTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE entity_image_catalog_entities (
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
		`CREATE TABLE entity_image_catalog_candidates (
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
			validation_attempts INTEGER NOT NULL DEFAULT 0,
			last_validation_at TEXT NOT NULL DEFAULT '',
			next_retry_at TEXT NOT NULL DEFAULT '',
			last_validation_error TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (canonical_entity_id, provider, source_url),
			FOREIGN KEY (canonical_entity_id) REFERENCES entity_image_catalog_entities(canonical_entity_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE entity_image_catalog_materializations (
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply test schema: %v", err)
		}
	}
	return db
}

func TestSQLiteEntityImageCatalogRoundTripAndIdentitySeparation(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	ctx := context.Background()

	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: "PERSON:Michael-Jordan",
		EntityType:        "person",
		CanonicalName:     "Michael Jordan",
	}); err != nil {
		t.Fatalf("upsert Michael Jordan: %v", err)
	}
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: "",
		EntityType:        "PERSON",
		CanonicalName:     "  MICHAEL   JORDAN  ",
	}); err != nil {
		t.Fatalf("upsert equivalent Michael Jordan variant: %v", err)
	}
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: "person:michael-b--jordan",
		EntityType:        "PERSON",
		CanonicalName:     "Michael B. Jordan",
	}); err != nil {
		t.Fatalf("upsert Michael B. Jordan: %v", err)
	}

	jordan, err := repo.GetEntity(ctx, " person:michael-jordan ")
	if err != nil {
		t.Fatalf("get Michael Jordan: %v", err)
	}
	if jordan.CanonicalEntityID != "person:michael-jordan" || jordan.EntityType != "PERSON" {
		t.Fatalf("normalized entity = %+v", jordan)
	}
	other, err := repo.GetEntity(ctx, "person:michael-b--jordan")
	if err != nil {
		t.Fatalf("get Michael B. Jordan: %v", err)
	}
	if other.CanonicalEntityID == jordan.CanonicalEntityID {
		t.Fatal("distinct PERSON identities collided")
	}

	firstID, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
		CanonicalEntityID: jordan.CanonicalEntityID, Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://images.example/michael-jordan-1.jpg", Width: 1920, Height: 1080,
	})
	if err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	secondID, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
		CanonicalEntityID: jordan.CanonicalEntityID, Provider: "duckduckgo", Rank: 2,
		SourceURL: "https://images.example/michael-jordan-2.jpg", Width: 1280, Height: 720,
	})
	if err != nil {
		t.Fatalf("upsert second candidate: %v", err)
	}
	if firstID == secondID {
		t.Fatal("different candidate URLs share an ID")
	}

	duplicateID, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
		CanonicalEntityID: "PERSON:MICHAEL-JORDAN", Provider: "duckduckgo", Rank: 3,
		SourceURL: "https://images.example/michael-jordan-1.jpg", ThumbnailURL: "https://images.example/thumb.jpg",
		Width: 800, Height: 600,
	})
	if err != nil {
		t.Fatalf("upsert duplicate candidate: %v", err)
	}
	if duplicateID != firstID {
		t.Fatalf("duplicate candidate id = %d, want existing %d", duplicateID, firstID)
	}

	candidates, err := repo.ListCandidates(ctx, jordan.CanonicalEntityID, 10)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 2 || candidates[0].Rank != 2 || candidates[1].Rank != 3 || candidates[1].Width != 800 {
		t.Fatalf("candidates = %+v, want deduplicated updated rows ordered by rank", candidates)
	}

	materializedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	verifiedAt := materializedAt.Add(time.Minute)
	if err := repo.UpsertMaterialization(ctx, entitycatalog.Materialization{
		CandidateID: firstID, AssetID: "asset-mj-1", LegacyFileMD5: "sha-mj-1",
		DriveFileID: "drive-mj-1", DriveLink: "https://drive.google.com/file/d/drive-mj-1/view",
		LocalPath: "/tmp/mj-1.jpg", Status: entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: materializedAt, LastVerifiedAt: verifiedAt,
	}); err != nil {
		t.Fatalf("upsert materialization: %v", err)
	}
	materialization, err := repo.GetMaterialization(ctx, firstID)
	if err != nil {
		t.Fatalf("get materialization: %v", err)
	}
	if materialization == nil || materialization.AssetID != "asset-mj-1" || materialization.DriveFileID != "drive-mj-1" {
		t.Fatalf("materialization = %+v", materialization)
	}

	if err := repo.SetRefreshState(ctx, jordan.CanonicalEntityID, entitycatalog.RefreshStatusSucceeded, materializedAt, ""); err != nil {
		t.Fatalf("set refresh state: %v", err)
	}
	jordan, err = repo.GetEntity(ctx, jordan.CanonicalEntityID)
	if err != nil {
		t.Fatalf("get refreshed entity: %v", err)
	}
	if jordan.RefreshStatus != entitycatalog.RefreshStatusSucceeded || jordan.LastRefreshAt.IsZero() {
		t.Fatalf("refresh metadata = %+v", jordan)
	}
}

func TestSQLiteEntityImageCatalogValidationAndCascade(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()

	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{CanonicalEntityID: "org:tesla", EntityType: "ORG", CanonicalName: "Tesla"}); err == nil {
		t.Fatal("expected non-PERSON entity to be rejected")
	}
	if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{CanonicalEntityID: "person:missing", Provider: "duckduckgo", Rank: 1, SourceURL: "https://example.test/a.jpg"}); err == nil {
		t.Fatal("expected candidate for missing entity to fail foreign-key validation")
	}
	if _, err := repo.GetEntity(ctx, "person:missing"); !errors.Is(err, entitycatalog.ErrEntityNotFound) {
		t.Fatalf("missing entity error = %v, want ErrEntityNotFound", err)
	}

	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{CanonicalEntityID: "person:ada-lovelace", EntityType: "PERSON", CanonicalName: "Ada Lovelace"}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{CanonicalEntityID: "person:ada-lovelace", Provider: "duckduckgo", Rank: 1, SourceURL: "https://example.test/ada.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(ctx, entitycatalog.Materialization{CandidateID: candidateID, Status: entitycatalog.MaterializationStatusFailed, LastError: "broken origin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM entity_image_catalog_entities WHERE canonical_entity_id = 'person:ada-lovelace'`); err != nil {
		t.Fatal(err)
	}
	if materialization, err := repo.GetMaterialization(ctx, candidateID); err != nil || materialization != nil {
		t.Fatalf("cascade materialization = %+v, err=%v", materialization, err)
	}
}

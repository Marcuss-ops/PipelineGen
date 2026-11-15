package entitycatalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
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
			source_page_url TEXT NOT NULL DEFAULT '',
			thumbnail_url TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
			height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
			status TEXT NOT NULL DEFAULT 'fresh' CHECK (status IN ('fresh','active','stale','broken','retired')),
			semantic_status TEXT NOT NULL DEFAULT 'unknown' CHECK (semantic_status IN ('unknown','accepted','rejected')),
			semantic_score REAL NOT NULL DEFAULT 0 CHECK (semantic_score >= 0 AND semantic_score <= 1),
			technical_score REAL NOT NULL DEFAULT 0 CHECK (technical_score >= 0 AND technical_score <= 1),
			quality_reason TEXT NOT NULL DEFAULT '',
			content_sha256 TEXT NOT NULL DEFAULT '',
			verifier_version TEXT NOT NULL DEFAULT '',
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
			content_sha256 TEXT NOT NULL DEFAULT '',
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
		CREATE TABLE entity_image_catalog_materialization_attempts (
			attempt_id INTEGER PRIMARY KEY AUTOINCREMENT,
			candidate_id INTEGER NOT NULL,
			asset_id TEXT NOT NULL DEFAULT '',
			content_sha256 TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			attempted_at TEXT NOT NULL,
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

	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: "PERSON:Michael-Jordan",
		EntityType:        "person",
		CanonicalName:     "Michael Jordan",
	}); err != nil {
		t.Fatalf("upsert Michael Jordan: %v", err)
	}
	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: "",
		EntityType:        "PERSON",
		CanonicalName:     "  MICHAEL   JORDAN  ",
	}); err != nil {
		t.Fatalf("upsert equivalent Michael Jordan variant: %v", err)
	}
	if err := repo.UpsertEntity(ctx, capentity.Entity{
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

	firstID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: jordan.CanonicalEntityID, Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://images.example/michael-jordan-1.jpg", Width: 1920, Height: 1080,
	})
	if err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	secondID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: jordan.CanonicalEntityID, Provider: "duckduckgo", Rank: 2,
		SourceURL: "https://images.example/michael-jordan-2.jpg", Width: 1280, Height: 720,
	})
	if err != nil {
		t.Fatalf("upsert second candidate: %v", err)
	}
	if firstID == secondID {
		t.Fatal("different candidate URLs share an ID")
	}

	duplicateID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
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
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{
		CandidateID: firstID, AssetID: "asset-mj-1", ContentSHA256: canonicalTestContentSHA,
		DriveFileID: "drive-mj-1", DriveLink: "https://drive.google.com/file/d/drive-mj-1/view",
		Status:         capentity.MaterializationStatusMaterialized,
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

	if err := repo.SetRefreshState(ctx, jordan.CanonicalEntityID, capentity.RefreshStatusSucceeded, materializedAt, ""); err != nil {
		t.Fatalf("set refresh state: %v", err)
	}
	jordan, err = repo.GetEntity(ctx, jordan.CanonicalEntityID)
	if err != nil {
		t.Fatalf("get refreshed entity: %v", err)
	}
	if jordan.RefreshStatus != capentity.RefreshStatusSucceeded || jordan.LastRefreshAt.IsZero() {
		t.Fatalf("refresh metadata = %+v", jordan)
	}
}

func TestSQLiteEntityImageCatalogValidationAndCascade(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()

	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "org:tesla", EntityType: "ORG", CanonicalName: "Tesla"}); err == nil {
		t.Fatal("expected non-PERSON entity to be rejected")
	}
	if _, err := repo.UpsertCandidate(ctx, capentity.Candidate{CanonicalEntityID: "person:missing", Provider: "duckduckgo", Rank: 1, SourceURL: "https://example.test/a.jpg"}); err == nil {
		t.Fatal("expected candidate for missing entity to fail foreign-key validation")
	}
	if _, err := repo.GetEntity(ctx, "person:missing"); !errors.Is(err, capentity.ErrEntityNotFound) {
		t.Fatalf("missing entity error = %v, want ErrEntityNotFound", err)
	}

	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:ada-lovelace", EntityType: "PERSON", CanonicalName: "Ada Lovelace"}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(ctx, capentity.Candidate{CanonicalEntityID: "person:ada-lovelace", Provider: "duckduckgo", Rank: 1, SourceURL: "https://example.test/ada.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{CandidateID: candidateID, Status: capentity.MaterializationStatusFailed, LastError: "broken origin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM entity_image_catalog_entities WHERE canonical_entity_id = 'person:ada-lovelace'`); err != nil {
		t.Fatal(err)
	}
	if materialization, err := repo.GetMaterialization(ctx, candidateID); err != nil || materialization != nil {
		t.Fatalf("cascade materialization = %+v, err=%v", materialization, err)
	}
}

// TestSQLiteEntityImageCatalogUpsertPreservesDurableVerdicts pins the
// no-downgrade contract of UpsertCandidate: a discovery re-observation that
// carries NO pixel verdict (semantic_status unknown) must never clobber a
// durable accepted/rejected verdict, while a real verification outcome still
// rewrites it. Without the preservation, every catalog-cache replay and
// provider refresh reset the row to unknown, IsUsableCandidateQuality then
// failed, and the pool forced a provider re-search plus a full download+VLM
// re-verification of pixels that were already certified.
func TestSQLiteEntityImageCatalogUpsertPreservesDurableVerdicts(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	ctx := context.Background()

	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:chiara-poggi", EntityType: "PERSON", CanonicalName: "Chiara Poggi"}); err != nil {
		t.Fatal(err)
	}
	const url = "https://images.example/chiara-poggi-1.jpg"

	// First observation comes from the pixel verifier: accepted, scored.
	id, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:chiara-poggi", Provider: "duckduckgo", Rank: 1,
		SourceURL: url, SemanticStatus: capentity.CandidateSemanticAccepted,
		SemanticScore: 0.92, QualityReason: "Ollama visual verification confirmed one dominant subject",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A later discovery pass re-observes the same URL with NO verdict
	// (catalog-cache replay or provider refresh). The durable verdict,
	// its score and its reason must survive untouched.
	if _, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:chiara-poggi", Provider: "duckduckgo", Rank: 4,
		SourceURL: url,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(ctx, "person:chiara-poggi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("candidates = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.ID != id {
		t.Fatalf("candidate id = %d, want %d", got.ID, id)
	}
	if got.SemanticStatus != capentity.CandidateSemanticAccepted || got.SemanticScore != 0.92 {
		t.Fatalf("verdict = %s/%v, want accepted/0.92 preserved", got.SemanticStatus, got.SemanticScore)
	}
	if got.QualityReason != "Ollama visual verification confirmed one dominant subject" {
		t.Fatalf("quality reason = %q, want the stored reason preserved", got.QualityReason)
	}
	if got.Rank != 4 {
		t.Fatalf("rank = %d, want the discovery rank updated to 4", got.Rank)
	}

	// A real verification outcome (accepted -> rejected) still rewrites
	// the verdict: preservation applies only to unknown writes.
	if _, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:chiara-poggi", Provider: "duckduckgo", Rank: 5,
		SourceURL: url, SemanticStatus: capentity.CandidateSemanticRejected,
		QualityReason: "Ollama visual verification detected a split panel or collage",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListCandidates(ctx, "person:chiara-poggi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].SemanticStatus != capentity.CandidateSemanticRejected || rows[0].SemanticScore != 0 {
		t.Fatalf("verdict = %s/%v, want rejected/0 from a real verification outcome", rows[0].SemanticStatus, rows[0].SemanticScore)
	}
	if rows[0].QualityReason != "Ollama visual verification detected a split panel or collage" {
		t.Fatalf("quality reason = %q, want the rejection reason", rows[0].QualityReason)
	}

	// A fresh accepted outcome upgrades the rejection again.
	if _, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:chiara-poggi", Provider: "duckduckgo", Rank: 6,
		SourceURL: url, SemanticStatus: capentity.CandidateSemanticAccepted, SemanticScore: 0.8,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListCandidates(ctx, "person:chiara-poggi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].SemanticStatus != capentity.CandidateSemanticAccepted || rows[0].SemanticScore != 0.8 {
		t.Fatalf("verdict = %s/%v, want accepted/0.8 after re-certification", rows[0].SemanticStatus, rows[0].SemanticScore)
	}
}

func TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	ctx := context.Background()
	const entityID = "person:michael-jordan"
	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: entityID,
		EntityType:        capentity.EntityTypePerson,
		CanonicalName:     "Michael Jordan",
	}); err != nil {
		t.Fatal(err)
	}

	url := "https://images.example/michael-jordan/verdict.jpg"
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	base := capentity.Candidate{
		CanonicalEntityID: entityID,
		Provider:          "wikimedia_commons",
		Rank:              1,
		SourceURL:         url,
		Status:            capentity.CandidateStatusFresh,
		SemanticStatus:    capentity.CandidateSemanticAccepted,
		SemanticScore:     0.9,
		TechnicalScore:    0.9,
		QualityReason:     "Ollama visual identity verification confirmed the requested person and one dominant subject",
		ContentSHA256:     hashA,
		VerifierVersion:   capentity.CandidateVerifierVersion,
	}
	if _, err := repo.UpsertCandidate(ctx, base); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(ctx, entityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v, want 1", len(rows), err)
	}
	if rows[0].Provider != "wikimedia_commons" || rows[0].ContentSHA256 != hashA || rows[0].VerifierVersion != capentity.CandidateVerifierVersion {
		t.Fatalf("identity = provider=%q content=%q verifier=%q, want wikimedia_commons/%s/%s",
			rows[0].Provider, rows[0].ContentSHA256, rows[0].VerifierVersion, hashA, capentity.CandidateVerifierVersion)
	}

	// The same URL resolving to different bytes must not keep a verdict about
	// the old bytes. A discovery write carries no new verdict, so it invalidates.
	changed := base
	changed.ContentSHA256 = hashB
	changed.SemanticStatus = capentity.CandidateSemanticUnknown
	changed.SemanticScore = 0
	changed.QualityReason = ""
	changed.VerifierVersion = ""
	if _, err := repo.UpsertCandidate(ctx, changed); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListCandidates(ctx, entityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v, want 1 after content change", len(rows), err)
	}
	if rows[0].ContentSHA256 != hashB || rows[0].SemanticStatus != capentity.CandidateSemanticUnknown ||
		rows[0].VerifierVersion != "" || !strings.Contains(rows[0].QualityReason, "content changed") {
		t.Fatalf("content change = content=%q verdict=%q verifier=%q reason=%q",
			rows[0].ContentSHA256, rows[0].SemanticStatus, rows[0].VerifierVersion, rows[0].QualityReason)
	}

	// A fresh verdict for the new bytes is a real re-certification and must be
	// stored together with its content identity.
	changed.SemanticStatus = capentity.CandidateSemanticAccepted
	changed.SemanticScore = 0.9
	changed.QualityReason = "Ollama visual identity verification confirmed the requested person and one dominant subject"
	changed.VerifierVersion = capentity.CandidateVerifierVersion
	if _, err := repo.UpsertCandidate(ctx, changed); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListCandidates(ctx, entityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v, want 1 after re-certification", len(rows), err)
	}
	if rows[0].ContentSHA256 != hashB || rows[0].SemanticStatus != capentity.CandidateSemanticAccepted ||
		rows[0].VerifierVersion != capentity.CandidateVerifierVersion {
		t.Fatalf("re-certification = content=%q verdict=%q verifier=%q",
			rows[0].ContentSHA256, rows[0].SemanticStatus, rows[0].VerifierVersion)
	}

	// The materializer is the authoritative byte producer. If it yields bytes
	// other than the verified ones, the verdict must be invalidated.
	hashC := strings.Repeat("d", 64)
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{
		CandidateID:   rows[0].ID,
		AssetID:       "asset-mj",
		ContentSHA256: hashC,
		DriveLink:     "https://drive.google.com/file/d/asset-mj/view",
		Status:        capentity.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListCandidates(ctx, entityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v, want 1 after materialization", len(rows), err)
	}
	if rows[0].ContentSHA256 != hashC || rows[0].SemanticStatus != capentity.CandidateSemanticUnknown ||
		rows[0].VerifierVersion != "" || !strings.Contains(rows[0].QualityReason, "materialization bytes differ") {
		t.Fatalf("materialization content change = content=%q verdict=%q verifier=%q reason=%q",
			rows[0].ContentSHA256, rows[0].SemanticStatus, rows[0].VerifierVersion, rows[0].QualityReason)
	}
}

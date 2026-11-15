package entitycatalog

import (
	"context"
	"testing"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestSQLiteEntityImageCatalogRecertificationPreservesDriveAsset(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:michael-jordan", EntityType: capentity.EntityTypePerson, CanonicalName: "Michael Jordan"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-capentity.CandidateFreshAfter - time.Hour)
	candidateID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:michael-jordan", Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://images.example/mj.png", Status: capentity.CandidateStatusStale,
		SemanticStatus: capentity.CandidateSemanticAccepted, LastSeenAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldValue := old.Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`UPDATE entity_image_catalog_candidates SET last_seen_at = ?, status = 'stale' WHERE candidate_id = ?`, oldValue, candidateID); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{

		CandidateID: candidateID, AssetID: "asset-mj", ContentSHA256: canonicalTestContentSHA,
		DriveFileID: "drive-mj", DriveLink: "https://drive.google.com/file/d/drive-mj/view",
		Status: capentity.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}

	recertRepo, ok := repo.(capentity.RecertificationRepository)
	if !ok {
		t.Fatal("SQLite adapter does not implement recertification repository")
	}
	items, err := recertRepo.ListCandidatesForRecertification(ctx, time.Now().UTC(), 10, 5)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	// The work set must carry the DISCOVERY row only: a recertification batch
	// is not a reader of the durable media identity, so it must not rehydrate
	// asset_id / drive / digest metadata into the candidate. The durable
	// identity is asserted below (after the remote failure) against its own
	// owner instead.
	checkedAt := time.Now().UTC()
	if err := recertRepo.RecordCandidateValidation(ctx, candidateID, capentity.ValidationResult{CheckedAt: checkedAt, Success: false, FailureCount: 1, Error: "HTTP 503", NextRetryAt: checkedAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	items, err = recertRepo.ListCandidatesForRecertification(ctx, checkedAt.Add(30*time.Minute), 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("broken candidate selected before next_retry_at: %d", len(items))
	}
	items, err = recertRepo.ListCandidatesForRecertification(ctx, checkedAt.Add(2*time.Hour), 10, 5)
	if err != nil || len(items) != 1 {
		t.Fatalf("retry selection items=%d err=%v", len(items), err)
	}

	materialization, err := repo.GetMaterialization(ctx, candidateID)
	if err != nil || materialization == nil || materialization.DriveFileID != "drive-mj" || materialization.Status != capentity.MaterializationStatusMaterialized {
		t.Fatalf("Drive asset changed after remote failure: %+v err=%v", materialization, err)
	}
}

// TestSQLiteEntityImageCatalogRecertificationRecoversBrokenURL pins the
// dead-end fix: a successful URL probe must restore the candidate to fresh and
// clear its retry budget. The materialization row is intentionally untouched;
// the common finalizer re-acquires the URL if the old Drive asset was broken.
func TestSQLiteEntityImageCatalogRecertificationRecoversBrokenURL(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:michael-jordan", EntityType: capentity.EntityTypePerson, CanonicalName: "Michael Jordan"}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:michael-jordan",
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan-recovered.png",
		Status:            capentity.CandidateStatusBroken,
		SemanticStatus:    capentity.CandidateSemanticAccepted,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Now().UTC()
	recertRepo, ok := repo.(capentity.RecertificationRepository)
	if !ok {
		t.Fatal("SQLite adapter does not implement recertification repository")
	}
	if err := recertRepo.RecordCandidateValidation(ctx, candidateID, capentity.ValidationResult{CheckedAt: checkedAt, Success: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(ctx, "person:michael-jordan", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != capentity.CandidateStatusFresh {
		t.Fatalf("recovered candidate = %+v, want status fresh", rows)
	}
	var attempts int
	var nextRetry string
	if err := db.QueryRow(`SELECT validation_attempts, next_retry_at FROM entity_image_catalog_candidates WHERE candidate_id = ?`, candidateID).Scan(&attempts, &nextRetry); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || nextRetry != "" {
		t.Fatalf("retry state attempts=%d next_retry_at=%q, want 0/empty", attempts, nextRetry)
	}
}

// TestSQLiteEntityImageCatalogUpsertCandidateReturnsStableID covers the
// transactional INSERT+SELECT path: a repeated discovery of the same identity
// must return the same row id, not a second row from a racing writer.
func TestSQLiteEntityImageCatalogUpsertCandidateReturnsStableID(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:michael-jordan", EntityType: capentity.EntityTypePerson, CanonicalName: "Michael Jordan"}); err != nil {
		t.Fatal(err)
	}
	candidate := capentity.Candidate{
		CanonicalEntityID: "person:michael-jordan",
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan-stable.png",
		Status:            capentity.CandidateStatusFresh,
		SemanticStatus:    capentity.CandidateSemanticUnknown,
	}
	first, err := repo.UpsertCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.UpsertCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == 0 {
		t.Fatalf("upsert ids = %d/%d, want the same non-zero row", first, second)
	}
}

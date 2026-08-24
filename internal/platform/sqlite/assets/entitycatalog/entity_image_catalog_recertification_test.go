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

		CandidateID: candidateID, AssetID: "asset-mj", LegacyFileMD5: "sha-mj",
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
	if items[0].Materialization == nil || items[0].Materialization.DriveFileID != "drive-mj" {
		t.Fatalf("materialization=%+v", items[0].Materialization)
	}

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

package entitycatalog

import (
	"context"
	"strings"
	"testing"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestUpsertMaterializationAppendsAttemptHistory(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	adapter := NewSQLiteEntityImageCatalogAdapter(db)
	repo := adapter
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{CanonicalEntityID: "person:history-person", EntityType: capentity.EntityTypePerson, CanonicalName: "History Person"}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:history-person", Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://example.com/history.jpg", Status: capentity.CandidateStatusFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{
		CandidateID: candidateID, Status: capentity.MaterializationStatusFailed, LastError: "origin 503",
	}); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("c", 64)
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{
		CandidateID: candidateID, AssetID: "asset-history", ContentSHA256: hash,
		DriveLink: "https://drive.example/history", Status: capentity.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}
	attempts, err := repo.(*SQLiteEntityImageCatalogAdapter).ListMaterializationAttempts(ctx, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want both writes retained", attempts)
	}
	if attempts[0].Status != capentity.MaterializationStatusFailed || attempts[0].LastError != "origin 503" {
		t.Fatalf("first attempt = %+v, want the failed write", attempts[0])
	}
	if attempts[1].Status != capentity.MaterializationStatusMaterialized || attempts[1].ContentSHA256 != hash {
		t.Fatalf("second attempt = %+v, want the successful write", attempts[1])
	}
}

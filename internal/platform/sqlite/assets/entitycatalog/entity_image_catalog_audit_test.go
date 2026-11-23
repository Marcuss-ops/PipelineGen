package entitycatalog

import (
	"context"
	"testing"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

// TestListCatalogAuditRowsJoinsEntityAndMaterialization pins the projection the
// audit depends on: the entity display name comes from the entity table, a
// materialization contributes its status and content address, and a candidate
// without one is returned rather than dropped.
func TestListCatalogAuditRowsJoinsEntityAndMaterialization(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	ctx := context.Background()

	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: "person:corrado-augias",
		EntityType:        "PERSON",
		CanonicalName:     "Corrado Augias",
	}); err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	withMaterialization, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:corrado-augias",
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://img.example/augias.jpg",
		Status:            capentity.CandidateStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(ctx, capentity.Materialization{
		CandidateID:   withMaterialization,
		AssetID:       "asset-augias",
		ContentSHA256: sha,
		DriveLink:     "https://drive.example/augias",
		Status:        capentity.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:corrado-augias",
		Provider:          "duckduckgo",
		Rank:              2,
		SourceURL:         "https://img.example/augias-2.jpg",
		Status:            capentity.CandidateStatusActive,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := NewSQLiteEntityImageCatalogAdapter(db).(*SQLiteEntityImageCatalogAdapter).ListCatalogAuditRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (a candidate without a materialization must still be read)", len(rows))
	}
	first, second := rows[0], rows[1]
	if first.EntityName != "Corrado Augias" || second.EntityName != "Corrado Augias" {
		t.Fatalf("entity name projection missing: %+v", rows)
	}
	if first.MaterializationSHA != sha || first.MaterializationStatus != capentity.MaterializationStatusMaterialized {
		t.Fatalf("materialization projection = %+v", first)
	}
	if second.MaterializationSHA != "" || second.MaterializationStatus != "" {
		t.Fatalf("candidate without materialization must project empty fields: %+v", second)
	}
	if first.SourceURL == "" || first.CandidateStatus == "" {
		t.Fatalf("candidate projection = %+v", first)
	}
}

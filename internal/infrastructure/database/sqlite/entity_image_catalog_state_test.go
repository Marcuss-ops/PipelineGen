package sqlite

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
)

func TestSQLiteEntityImageCatalogRetiredCandidateIsTerminalOnProviderUpsert(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	ctx := context.Background()
	identity := entitycatalog.PersonIdentity{CanonicalEntityID: "person:ada-lovelace", CanonicalName: "Ada Lovelace"}
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	candidate := entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID,
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/ada.jpg",
		Status:            entitycatalog.CandidateStatusRetired,
		SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
	}
	if _, err := repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Status = entitycatalog.CandidateStatusFresh
	if _, err := repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, err=%v", len(rows), err)
	}
	if rows[0].Status != entitycatalog.CandidateStatusRetired {
		t.Fatalf("retired candidate status = %q, want %q", rows[0].Status, entitycatalog.CandidateStatusRetired)
	}
}

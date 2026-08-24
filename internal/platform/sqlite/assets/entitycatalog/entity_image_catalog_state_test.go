package entitycatalog

import (
	"context"
	"testing"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestSQLiteEntityImageCatalogRetiredCandidateIsTerminalOnProviderUpsert(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	ctx := context.Background()
	identity := capentity.PersonIdentity{CanonicalEntityID: "person:ada-lovelace", CanonicalName: "Ada Lovelace"}
	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        capentity.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	candidate := capentity.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID,
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/ada.jpg",
		Status:            capentity.CandidateStatusRetired,
		SemanticStatus:    capentity.CandidateSemanticAccepted,
	}
	if _, err := repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Status = capentity.CandidateStatusFresh
	if _, err := repo.UpsertCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, err=%v", len(rows), err)
	}
	if rows[0].Status != capentity.CandidateStatusRetired {
		t.Fatalf("retired candidate status = %q, want %q", rows[0].Status, capentity.CandidateStatusRetired)
	}
}

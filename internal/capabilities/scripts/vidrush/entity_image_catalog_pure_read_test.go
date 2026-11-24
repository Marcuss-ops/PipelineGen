package vidrush

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestEntityImageCatalogLookupDoesNotMutateCandidates(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEntity(context.Background(), entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: entitycatalog.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 1, SourceURL: "https://images.example/john-gotti.jpg", Status: entitycatalog.CandidateStatusFresh,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted,
		// Pinned read-only fixture: it exists to prove the lookup is pure, not
		// to claim pixel verification.
		VerifierVersion: "pinned-read-fixture", QualityReason: "pinned test fixture: read purity",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted || rows[0].QualityReason != "pinned test fixture: read purity" {
		t.Fatalf("catalog lookup mutated candidate: %+v", rows)
	}
}

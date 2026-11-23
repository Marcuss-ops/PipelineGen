package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestEntityImageCatalogPoolRecordsPerCandidateDecisions(t *testing.T) {
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
	acceptedID, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://images.example/michael-jordan.jpg", Status: entitycatalog.CandidateStatusFresh,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, VerifierVersion: entitycatalog.CandidateVerifierVersion,
		QualityReason: "Ollama visual identity verification confirmed one dominant subject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID: acceptedID, AssetID: "michael-jordan", ContentSHA256: strings.Repeat("a", 64),
		DriveLink: "https://drive.google.com/file/d/michael-jordan/view", Status: entitycatalog.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 2,
		SourceURL: "https://images.example/michael-someone.jpg", Status: entitycatalog.CandidateStatusFresh,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, VerifierVersion: entitycatalog.CandidateVerifierVersion,
		QualityReason: "Ollama visual identity verification confirmed one dominant subject",
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Decisions) != 2 {
		t.Fatalf("decisions = %+v, want one per candidate", pool.Decisions)
	}
	if pool.Decisions[0].Selection != "pool" || pool.Decisions[0].Gate != "quality" || pool.Decisions[0].Materialization != "materialized" {
		t.Fatalf("accepted decision = %+v", pool.Decisions[0])
	}
	if pool.Decisions[1].Selection != "rejected" || pool.Decisions[1].Gate != "identity" {
		t.Fatalf("identity rejection decision = %+v", pool.Decisions[1])
	}
}

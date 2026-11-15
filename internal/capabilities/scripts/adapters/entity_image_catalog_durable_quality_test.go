package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestDurableMaterializationDoesNotBypassCatalogQualityGate(t *testing.T) {
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
	candidateID, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID,
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan.jpg",
		Status:            entitycatalog.CandidateStatusFresh,
		SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
		// Explicitly legacy: this fixture never passed the pixel verifier.
		VerifierVersion: "pinned-legacy-metadata-only",
		QualityReason:   "pinned legacy fixture: metadata-only name match",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID: candidateID, AssetID: "drive-michael-jordan", ContentSHA256: strings.Repeat("a", 64),
		DriveLink: "https://drive.google.com/file/d/drive-michael-jordan/view",
		Status:    entitycatalog.MaterializationStatusMaterialized,
	}); err != nil {
		t.Fatal(err)
	}

	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Candidates) != 0 {
		t.Fatalf("durable but unsupported quality verdict entered the pool: %+v", pool.Candidates)
	}
}

package entitycatalog

import (
	"context"
	"testing"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestPruneRetiredCandidatesRemovesOnlyExpiredRejectedOrRetired(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := NewSQLiteEntityImageCatalogAdapter(db)
	identity := capentity.PersonIdentity{CanonicalEntityID: "person:retention-person", CanonicalName: "Retention Person"}
	if err := repo.UpsertEntity(context.Background(), capentity.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: capentity.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	rows := []capentity.Candidate{
		{CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 1, SourceURL: "https://example.com/rejected.jpg", Status: capentity.CandidateStatusFresh, SemanticStatus: capentity.CandidateSemanticRejected},
		{CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 2, SourceURL: "https://example.com/retired.jpg", Status: capentity.CandidateStatusRetired},
		{CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 3, SourceURL: "https://example.com/active.jpg", Status: capentity.CandidateStatusActive},
	}
	for _, row := range rows {
		if _, err := repo.UpsertCandidate(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE entity_image_catalog_candidates SET updated_at = ? WHERE status != ?`, old.Format("2006-01-02 15:04:05"), capentity.CandidateStatusActive); err != nil {
		t.Fatal(err)
	}
	adapter, ok := repo.(*SQLiteEntityImageCatalogAdapter)
	if !ok {
		t.Fatalf("repository type = %T, want SQLite adapter", repo)
	}
	removed, err := adapter.PruneRetiredCandidates(context.Background(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	remaining, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].SourceURL != "https://example.com/active.jpg" {
		t.Fatalf("remaining candidates = %+v, want only the active row", remaining)
	}
}

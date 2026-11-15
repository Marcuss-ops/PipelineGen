package entitycatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestSetCandidateStatusRetiredIsTerminal(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := &SQLiteEntityImageCatalogAdapter{db: db}
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: "person:guard-test",
		EntityType:        "PERSON",
		CanonicalName:     "Guard Test",
	}); err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	id, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:guard-test",
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/guard-test.jpg",
		Status:            capentity.CandidateStatusFresh,
	})
	if err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	if err := repo.SetCandidateStatus(ctx, id, capentity.CandidateStatusRetired); err != nil {
		t.Fatalf("retire candidate: %v", err)
	}
	if err := repo.SetCandidateStatus(ctx, id, capentity.CandidateStatusFresh); !errors.Is(err, capentity.ErrInvalidCandidateTransition) {
		t.Fatalf("revive without explicit event error = %v, want %v", err, capentity.ErrInvalidCandidateTransition)
	}
}

func TestListCandidatesForRecertificationIncludesRejected(t *testing.T) {
	db := openEntityImageCatalogTestDB(t)
	repo := &SQLiteEntityImageCatalogAdapter{db: db}
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, capentity.Entity{
		CanonicalEntityID: "person:recheck-test",
		EntityType:        "PERSON",
		CanonicalName:     "Recheck Test",
	}); err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	id, err := repo.UpsertCandidate(ctx, capentity.Candidate{
		CanonicalEntityID: "person:recheck-test",
		Provider:          "duckduckgo",
		Rank:              1,
		SourceURL:         "https://images.example/recheck-test.jpg",
		Status:            capentity.CandidateStatusFresh,
		SemanticStatus:    capentity.CandidateSemanticRejected,
	})
	if err != nil {
		t.Fatalf("upsert rejected candidate: %v", err)
	}
	if err := repo.SetCandidateStatus(ctx, id, capentity.CandidateStatusBroken); err != nil {
		t.Fatalf("mark candidate broken: %v", err)
	}
	oldSeen := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`UPDATE entity_image_catalog_candidates
		SET last_seen_at = ?, validation_attempts = 0, next_retry_at = ''
		WHERE candidate_id = ?`, oldSeen, id); err != nil {
		t.Fatalf("age rejected candidate: %v", err)
	}
	items, err := repo.ListCandidatesForRecertification(ctx, time.Now().UTC(), 10, 3)
	if err != nil {
		t.Fatalf("list recertification candidates: %v", err)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("recertification candidates = %+v, want the rejected row", items)
	}
}

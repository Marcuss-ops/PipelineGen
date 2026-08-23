package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestEntityImageCatalogLookupPersistsFreshToStaleTransition(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/michael-jordan-state.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	repo.mu.Lock()
	for id, candidate := range repo.candidates {
		if candidate.CanonicalEntityID == identity.CanonicalEntityID {
			candidate.Status = entitycatalog.CandidateStatusFresh
			candidate.LastSeenAt = now.Add(-entitycatalog.CandidateFreshAfter - time.Second)
			repo.candidates[id] = candidate
		}
	}
	repo.mu.Unlock()

	pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !pool.Sufficient || len(pool.Candidates) != 1 {
		t.Fatalf("stale pool = %+v, want one usable sufficient candidate", pool)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("persisted rows = %d, err=%v", len(rows), err)
	}
	if rows[0].Status != entitycatalog.CandidateStatusStale {
		t.Fatalf("persisted state = %q, want %q", rows[0].Status, entitycatalog.CandidateStatusStale)
	}
}

package entitycatalog

import (
	"context"
	"strings"
	"testing"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestRefreshLeaseCanBeClaimedRecoveredAndReclaimed(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	identity := capentity.PersonIdentity{CanonicalEntityID: "person:lease-person", CanonicalName: "Lease Person"}
	if err := repo.UpsertEntity(context.Background(), capentity.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: capentity.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	adapter := repo.(*SQLiteEntityImageCatalogAdapter)
	now := time.Now().UTC()
	claimed, err := adapter.ClaimRefresh(context.Background(), identity.CanonicalEntityID, now, time.Hour)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = adapter.ClaimRefresh(context.Background(), identity.CanonicalEntityID, now.Add(10*time.Minute), time.Hour)
	if err != nil || claimed {
		t.Fatalf("live lease was stolen: %v, %v", claimed, err)
	}
	reclaimed, err := adapter.ClaimRefresh(context.Background(), identity.CanonicalEntityID, now.Add(2*time.Hour), time.Hour)
	if err != nil || !reclaimed {
		t.Fatalf("expired lease was not reclaimed: %v, %v", reclaimed, err)
	}
	recovered, err := adapter.RecoverStaleRefresh(context.Background(), now.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered rows = %d, want 1", recovered)
	}
	entity, err := adapter.GetEntity(context.Background(), identity.CanonicalEntityID)
	if err != nil {
		t.Fatal(err)
	}
	if entity.RefreshStatus != capentity.RefreshStatusFailed || !strings.Contains(entity.LastError, "lease expired") {
		t.Fatalf("entity refresh state = %q / %q, want failed with lease diagnostic", entity.RefreshStatus, entity.LastError)
	}
}

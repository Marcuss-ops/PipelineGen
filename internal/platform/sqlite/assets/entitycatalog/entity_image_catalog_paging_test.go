package entitycatalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func TestListCandidatesPageReadsPastCompatibilityCap(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	identity := capentity.PersonIdentity{CanonicalEntityID: "person:paging-person", CanonicalName: "Paging Person"}
	if err := repo.UpsertEntity(context.Background(), capentity.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: capentity.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	for rank := 1; rank <= 101; rank++ {
		if _, err := repo.UpsertCandidate(context.Background(), capentity.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: rank,
			SourceURL: fmt.Sprintf("https://example.com/paging-%d.jpg", rank), Status: capentity.CandidateStatusFresh,
		}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := repo.(*SQLiteEntityImageCatalogAdapter)
	page, err := adapter.ListCandidatesPage(context.Background(), identity.CanonicalEntityID, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Rank != 101 {
		t.Fatalf("page after offset 100 = %+v, want rank 101", page)
	}
	bounded, err := adapter.ListCandidates(context.Background(), identity.CanonicalEntityID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 100 {
		t.Fatalf("bounded ListCandidates returned %d rows, want the 100-row compatibility cap", len(bounded))
	}
}

func TestGetMaterializationsLoadsBatch(t *testing.T) {
	repo := NewSQLiteEntityImageCatalogAdapter(openEntityImageCatalogTestDB(t))
	identity := capentity.PersonIdentity{CanonicalEntityID: "person:batch-person", CanonicalName: "Batch Person"}
	if err := repo.UpsertEntity(context.Background(), capentity.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: capentity.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rank := 1; rank <= 2; rank++ {
		id, err := repo.UpsertCandidate(context.Background(), capentity.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: rank,
			SourceURL: fmt.Sprintf("https://example.com/batch-%d.jpg", rank), Status: capentity.CandidateStatusFresh,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err := repo.UpsertMaterialization(context.Background(), capentity.Materialization{
			CandidateID: id, AssetID: fmt.Sprintf("asset-%d", rank), ContentSHA256: strings.Repeat("a", 64),
			DriveLink: fmt.Sprintf("https://drive.example/%d", rank), Status: capentity.MaterializationStatusMaterialized,
			MaterializedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := repo.(*SQLiteEntityImageCatalogAdapter)
	got, err := adapter.GetMaterializations(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[ids[0]] == nil || got[ids[1]] == nil {
		t.Fatalf("batch materializations = %+v, want both rows", got)
	}
}

package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type failingCatalogRepository struct{ entitycatalog.Repository }

func (failingCatalogRepository) ListCandidates(context.Context, string, int) ([]entitycatalog.Candidate, error) {
	return nil, errors.New("catalog unavailable")
}

func TestResolveEntityImageCatalogAndCacheReleasesLockOnError(t *testing.T) {
	identity, err := entitycatalog.CanonicalizePersonName("Lock Test Person")
	if err != nil {
		t.Fatal(err)
	}
	fanout := &VidRushProviderFanout{catalog: failingCatalogRepository{}}
	plan := &scriptpkg.ResolvedGenerationPlan{}
	_, _, _, _, release, err := fanout.resolveEntityImageCatalogAndCache(
		context.Background(), plan, "Lock Test Person", "cache-key", identity, true,
	)
	if release != nil {
		defer release()
	}
	if err == nil {
		t.Fatal("expected the failing catalog read to surface")
	}
	acquired := make(chan struct{})
	go func() {
		release := entityImageLocks.Lock("entity-catalog:" + identity.CanonicalEntityID)
		close(acquired)
		release()
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("catalog lock was not released after the error path")
	}
}

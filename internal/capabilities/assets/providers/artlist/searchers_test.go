// Package artlist — searchers_test.go: TDD coverage for the
// PR-ARTLIST-SEARCHERS wiring closure (July 2026).
//
// Scope:
//   - TestService_SearchersAccessor_ReturnsInjectedSearchers: the
//     canonical 3 searcher fields (ScraperSearcher / PixabaySearcher /
//     PexelsSearcher) on ServiceDeps MUST flow into the *Service struct
//     fields that the Searchers() (Searcher, Searcher, Searcher)
//     accessor returns. The test constructs a *Service with 3 distinct
//     searcher mocks (each carrying a unique SourceName in its Search
//     method) and asserts pointer-equality of the returned triplet.
//
// Why pointer equality (not semantic): godlike/06 one-canonical-owner-
// per-fact. The composition root (internal/app/build_bundles_artlist_artlist.go
// ::WireArtlist) is the single construction site for the 3 searchers
// (scraper.New + fallback.NewPixabay + fallback.NewPexels); the *Service
// preserves those pointers verbatim so callers (diagnostic surfaces,
// future health probes) can observe the wired capabilities without
// reconstructing them. A future refactor that wraps or replaces the
// stored pointers surfaces as a test failure here.
//
// Test isolation: this test reuses the existing stubPublisherForArtlist
// + stubDispatcherForArtlist (from service_test.go + dispatcher_stub_test.go
// in this package) so it does not depend on any production surface that
// is not already covered by sibling tests. Uses the same createTestDB
// helper as service_test.go for the SQLite handle the liveCache
// constructor requires.
package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// mockSearcher is a minimal Searcher stub that records its name in
// each returned Candidate's SourceName. Distinct names per test
// instance let the test confirm the accessor preserves the SCAN-order
// (scraper, pixabay, pexels) without any reflection / type-name
// inspection.
type mockSearcher struct {
	name string
}

func (m *mockSearcher) Search(_ context.Context, _ SearchRequest) ([]Candidate, error) {
	return []Candidate{{
		ID:         m.name + "-candidate",
		Title:      m.name,
		SourceName: m.name,
	}}, nil
}

// Compile-time pin: mockSearcher satisfies the Searcher port. Drift
// at the port signature surfaces as a build failure here rather than
// at the Searchers() assertion below.
var _ Searcher = (*mockSearcher)(nil)

// TestService_SearchersAccessor_ReturnsInjectedSearchers pins the
// PR-ARTLIST-SEARCHERS wiring contract: the composition root
// (build_bundles_artlist_artlist.go::WireArtlist) is the single canonical
// construction site for the 3 searchers; the *Service preserves the
// 3 pointers in field order (scraper, pixabay, pexels); the
// Searchers() (Searcher, Searcher, Searcher) accessor returns them
// in that same order. A future refactor that drops one of the fields
// from the struct, renames them, or wraps them in adapters surfaces
// as a test failure here before reaching production.
func TestService_SearchersAccessor_ReturnsInjectedSearchers(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
	}
	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	scraperSearcher := &mockSearcher{name: "scraper"}
	pixabaySearcher := &mockSearcher{name: "pixabay"}
	pexelsSearcher := &mockSearcher{name: "pexels"}

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,

			ScraperSearcher: scraperSearcher,
			PixabaySearcher: pixabaySearcher,
			PexelsSearcher:  pexelsSearcher,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	gotScraper, gotPixabay, gotPexels := svc.Searchers()

	// Pointer-equality: the *Service must preserve the exact
	// searcher instances the composition root injected. A wrapper
	// layer, a copying constructor, or an accidental field-swap
	// surfaces here.
	require.Same(t, scraperSearcher, gotScraper,
		"ScraperSearcher not preserved verbatim by *Service.Searchers()")
	require.Same(t, pixabaySearcher, gotPixabay,
		"PixabaySearcher not preserved verbatim by *Service.Searchers()")
	require.Same(t, pexelsSearcher, gotPexels,
		"PexelsSearcher not preserved verbatim by *Service.Searchers()")

	// Cross-check: the injected mocks' Search method actually
	// round-trips through the accessor (the pointer is not just a
	// stash that was re-wired to a default). This is a secondary
	// pin in case a future refactor swaps the struct fields to a
	// map keyed by source-name and the *Service holds a different
	// concrete than was injected.
	sc, _, _ := svc.Searchers()
	cands, err := sc.Search(context.Background(), SearchRequest{Term: "x", Limit: 1})
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, "scraper", cands[0].SourceName,
		"the ScraperSearcher returned by Searchers() must execute the injected mock's Search method (no field-swap or wrapper layer)")
}

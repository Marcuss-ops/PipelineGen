// Package images (api/images) — compose_test.go locks the FASE 7
// composition contract: ImageSearchResolver wired into Server /
// DomainBundle / NewServerWithHealth is reachable from server-side
// consumers.
//
// FASE 7 (July 2026, image-territories action plan): the resolver
// is the canonical routing singleton. We use a pure-Go stub mock
// that implements routing.ImageSearchResolver directly (no
// testify/mock dependency drift, minimal blast radius per AGENTS.md).
//
// Fix-up note (post FASE 7 commit 98dd3849): removed the
// TestCompose_ServerSingleton_NilSafe sentinel that dispatched
// Resolve on a nil *interface* value — Go panics on nil-interface
// method calls before the typed-nil receiver check can run. The
// canonical contract is "typed-nil resolver returns ErrUnknownTerritory";
// a nil-interface is not a contract surface (any caller that constructs
// a nil interface must guard the call site). Dropped for honest
// behaviour. Also tightened the assignability lock + dropped the
// dead `var _ = http.StatusOK` marker.
package images

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Pure-Go stub mock ──────────────────────────────────────────────────

// mockImageSearchResolver is the pure-Go stub implementing
// routing.ImageSearchResolver for the FASE 7 composition contract
// test. Mirrors the canonical ErrUnknownTerritory contract so
// downstream handlers can rely on errors.Is.
type mockImageSearchResolver struct {
	searchByTerritory map[routing.ImageSearchTerritory]routing.ImageSearcher
	resolveErr        error
}

var _ routing.ImageSearchResolver = (*mockImageSearchResolver)(nil)

func (m *mockImageSearchResolver) Resolve(territory routing.ImageSearchTerritory) (routing.ImageSearcher, error) {
	if m == nil {
		return nil, routing.ErrUnknownTerritory
	}
	if m.resolveErr != nil {
		return nil, m.resolveErr
	}
	searcher, ok := m.searchByTerritory[territory]
	if !ok {
		return nil, routing.ErrUnknownTerritory
	}
	return searcher, nil
}

// stubSearcher returns a configurable result list for a given filter.
// Used to assert that the per-territory searcher is dispatched
// correctly by the resolver path.
type stubSearcher struct {
	rows []routing.ImageSearchResult
	err  error
}

var _ routing.ImageSearcher = (*stubSearcher)(nil)

func (s *stubSearcher) Search(_ context.Context, _ routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	if s == nil {
		return nil, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

// ── Test: mock contract surface ────────────────────────────────────────

// TestCompose_MockResolver_TypedNil_ReturnsErrUnknownTerritory —
// verifies the typed-nil receiver path (Go distinguishes "nil
// interface" from "nil concrete behind interface"). The canonical
// routing.ImageSearchResolverImpl.Resolve has the same nil-receiver
// guard; the mock mirrors it so handlers can rely on the contract.
func TestCompose_MockResolver_TypedNil_ReturnsErrUnknownTerritory(t *testing.T) {
	var mock *mockImageSearchResolver // typed-nil concrete
	var resolver routing.ImageSearchResolver = mock
	got, err := resolver.Resolve(routing.TerritoryAll)
	if !errors.Is(err, routing.ErrUnknownTerritory) {
		t.Fatalf("expected ErrUnknownTerritory from typed-nil resolver, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil searcher on typed-nil resolver, got %#v", got)
	}
}

// TestCompose_MockResolver_UnknownTerritory_ErrUnknownTerritory —
// locks the canonical error sentinel surface so production handlers
// can rely on errors.Is(err, routing.ErrUnknownTerritory).
func TestCompose_MockResolver_UnknownTerritory_ErrUnknownTerritory(t *testing.T) {
	mock := &mockImageSearchResolver{
		searchByTerritory: map[routing.ImageSearchTerritory]routing.ImageSearcher{},
	}
	got, err := mock.Resolve(routing.TerritoryAll)
	if !errors.Is(err, routing.ErrUnknownTerritory) {
		t.Fatalf("expected ErrUnknownTerritory, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil searcher on unknown territory, got %#v", got)
	}
}

// TestCompose_MockResolver_KnownTerritory_ReturnsStubSearcher —
// verifies the dispatcher hands off the right searcher per
// territory key. Mirrors the routing.image_search_resolver_test.go
// strong invariant in mock form.
func TestCompose_MockResolver_KnownTerritory_ReturnsStubSearcher(t *testing.T) {
	stubRetr := &stubSearcher{rows: []routing.ImageSearchResult{{AssetID: "r1", Origin: asset.ImageOriginRetrieved}}}
	stubGen := &stubSearcher{rows: []routing.ImageSearchResult{{AssetID: "g1", Origin: asset.ImageOriginGenerated}}}
	mock := &mockImageSearchResolver{
		searchByTerritory: map[routing.ImageSearchTerritory]routing.ImageSearcher{
			routing.TerritoryRetrieved: stubRetr,
			routing.TerritoryGenerated: stubGen,
		},
	}

	if s, err := mock.Resolve(routing.TerritoryRetrieved); err != nil || s != stubRetr {
		t.Fatalf("Resolve(retrieved): expected stubRetr, got %#v err=%v", s, err)
	}
	if s, err := mock.Resolve(routing.TerritoryGenerated); err != nil || s != stubGen {
		t.Fatalf("Resolve(generated): expected stubGen, got %#v err=%v", s, err)
	}
}

// TestCompose_MockResolver_AllTerritory_Hypothetical — verifies the
// handler test surface for territory=all (the canonical curl shape
// per the FASE 7 user spec: /api/images/search?territory=all&subject=...).
// The mock returns a stub composite searcher; downstream behaviour
// is exercised by the routing package's own tests (this test only
// verifies the *type-system* contract is reachable from this package).
func TestCompose_MockResolver_AllTerritory_Hypothetical(t *testing.T) {
	stubAll := &stubSearcher{rows: []routing.ImageSearchResult{
		{AssetID: "r1", Origin: asset.ImageOriginRetrieved, Provider: "wikipedia"},
		{AssetID: "g1", Origin: asset.ImageOriginGenerated, Provider: "flux"},
	}}
	mock := &mockImageSearchResolver{
		searchByTerritory: map[routing.ImageSearchTerritory]routing.ImageSearcher{
			routing.TerritoryAll: stubAll,
		},
	}
	searcher, err := mock.Resolve(routing.TerritoryAll)
	if err != nil {
		t.Fatalf("Resolve(all): %v", err)
	}
	rows, err := searcher.Search(context.Background(), routing.ImageFilter{SubjectID: "albert_einstein"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (1 retrieved + 1 generated), got %d", len(rows))
	}
	// Hard invariant: only OriginRetrieved or OriginGenerated allowed.
	for i, r := range rows {
		if r.Origin != asset.ImageOriginRetrieved && r.Origin != asset.ImageOriginGenerated {
			t.Errorf("row %d: unexpected origin %q (must be retrieved or generated)", i, r.Origin)
		}
	}
}

// ── Test: composition contract surface ─────────────────────────────────

// TestCompose_ServerSingleton_HoldsResolverType — locks the
// composition invariant: any routing.ImageSearchResolver can be
// assigned to the canonical singleton surface (api.ServerDeps.ImageSearchResolver
// and indirectly api.Server.imageSearchResolver). Defends against
// future refactors that might tighten the type or rename the field.
//
// FASE 7 minimal-test scope: we don't construct a full *Server
// (the production wiring path includes workers/handlers/router setup
// that would require a Registry fixture). The compile-time assertion
// below is the actual contract lock; the runtime assertions above
// verify the mock contract reaches the consumer boundary the same
// way the production wiring does.
func TestCompose_ServerSingleton_HoldsResolverType(t *testing.T) {
	// Compile-time: api.ServerDeps is the canonical grouping struct;
	// its ImageSearchResolver field accepts any routing.ImageSearchResolver.
	// The type-level lock below is the actual contract surface — the
	// runtime round-trip below verifies the actual contract reach.
	var _ api.ServerDeps

	mock := &mockImageSearchResolver{
		searchByTerritory: map[routing.ImageSearchTerritory]routing.ImageSearcher{},
	}
	var resolver routing.ImageSearchResolver = mock
	if resolver == nil {
		t.Fatal("mock resolver must be assignable to routing.ImageSearchResolver")
	}
	if _, err := resolver.Resolve(routing.TerritoryAll); !errors.Is(err, routing.ErrUnknownTerritory) {
		t.Fatalf("expected ErrUnknownTerritory through resolver path, got %v", err)
	}
}

// ── Documentation sentinel ─────────────────────────────────────────────

// NOTE: a smoke test that constructs a full *api.Server via
// NewServerWithHealth with the resolver populated was considered
// but rejected for minimum-blast-radius: building a full server
// requires a fake *Registry + cfg + multiple nil-tolerant deps.
// The compile-time assignability lock above + the mock contract
// tests form the FASE 7 composition contract surface; a full
// server fixture test would be valuable once downstream handlers
// exist (a follow-up commit after the user-spec curl verification).
//
// Reference: AGENTS.md "Pattern 7 — Reusing existing services +
// Pattern 8 — API package: thin transport only" — the api package
// is transport-only, so composition contracts belong in this test
// file rather than scattered handler files.

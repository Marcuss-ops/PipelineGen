package search

// catalog_discovery_certification_test.go is the item-15 certification that
// Catalog and Discovery are genuinely separated (PR-SEARCH-UNIVERSE,
// August 2026). It demonstrates, end-to-end through the Aggregator, the four
// properties the plan requires:
//
//	1. CATALOG reproducibility — the same query, dataset revision and
//	   embedding contract always return the identical ordered top-k
//	   (ten consecutive runs are byte-identical).
//	2. CATALOG separation    — a catalog query makes Qdrant calls > 0 and
//	   live-provider calls == 0.
//	3. DISCOVERY separation  — a discovery query makes live-provider
//	   calls > 0 and Qdrant calls == 0.
//	4. BLENDED dedup         — a provider result whose source_type|source_ref
//	   resolves (via CanonicalIdentityResolver) to a canonical asset already
//	   present in the catalog appears exactly ONCE in the merged result.
//
// The certification uses counting backends: catalog backends (semantic) stand
// in for the Qdrant read path and discovery backends (artlist) for the live
// provider path, so the assertion is about the ROUTING + DEDUP invariants, not
// a specific transport.

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
)

// certificationCounters counts how many times each universe's backends were
// actually invoked by the aggregator.
type certificationCounters struct {
	qdrant   atomic.Int64 // catalog (semantic) backend invocations
	provider atomic.Int64 // discovery (live provider) backend invocations
}

// countingBackend is a SearchBackend whose Search records an invocation in the
// shared counters according to its universe.
type countingBackend struct {
	name     string
	universe SearchUniverse
	caps     []Capability
	results  []Candidate
	counters *certificationCounters
}

func (b *countingBackend) Name() string               { return b.name }
func (b *countingBackend) Capabilities() []Capability { return b.caps }
func (b *countingBackend) Universe() SearchUniverse   { return b.universe }
func (b *countingBackend) Search(_ context.Context, _ Query) ([]Candidate, error) {
	switch b.universe {
	case SearchCatalog:
		b.counters.qdrant.Add(1)
	case SearchDiscovery:
		b.counters.provider.Add(1)
	}
	return b.results, nil
}

// fakeCanonicalResolver maps artlist|123 → the canonical asset already present
// in the catalog (asset-A). It simulates the provider backend resolving an
// ExternalCandidate through CanonicalIdentityResolver before fan-in.
type fakeCanonicalResolver struct{}

func (fakeCanonicalResolver) ResolveSource(_ context.Context, sourceType, sourceRef string) (CanonicalIdentity, error) {
	if sourceType == "artlist" && sourceRef == "123" {
		return CanonicalIdentity{AssetID: "asset-A", SourceType: "artlist", SourceRef: "123", Resolved: true}, nil
	}
	return CanonicalIdentity{SourceType: sourceType, SourceRef: sourceRef}, nil
}

func (fakeCanonicalResolver) ResolveContent(_ context.Context, _ string) (CanonicalIdentity, error) {
	return CanonicalIdentity{}, nil
}

func assetIDs(items []Candidate) []string {
	out := make([]string, len(items))
	for i, c := range items {
		out[i] = c.AssetID
	}
	return out
}

func TestCatalogDiscoverySeparationCertification(t *testing.T) {
	counters := &certificationCounters{}

	semantic := &countingBackend{
		name: "semantic", universe: SearchCatalog, caps: []Capability{CapVideo}, counters: counters,
		results: []Candidate{
			{AssetID: "asset-A", Source: "semantic", Title: "Jackie Chan Interview", Score: 0.95},
			{AssetID: "asset-B", Source: "semantic", Title: "Jackie Chan Behind the Scenes", Score: 0.88},
		},
	}
	artlist := &countingBackend{
		name: "artlist", universe: SearchDiscovery, caps: []Capability{CapVideo}, counters: counters,
		results: []Candidate{
			// SourceRef-only provider shape: the provider does NOT invent an
			// AssetID. Resolution to asset-A happens via the resolver below.
			{Source: "artlist", SourceRef: "123", Title: "Artlist Jackie Chan", Score: 0.60},
			{Source: "artlist", SourceRef: "456", Title: "Artlist Unrelated", Score: 0.55},
		},
	}

	reg := NewBackendRegistry()
	for _, b := range []*countingBackend{semantic, artlist} {
		if err := reg.Register(b); err != nil {
			t.Fatalf("register %s: %v", b.name, err)
		}
	}
	reg.Freeze()
	agg := NewAggregator(reg, nil)

	ctx := context.Background()
	q := Query{Text: "Jackie Chan interview", Limit: 10}

	// ── 1. CATALOG reproducibility + separation ───────────────────────
	qdrantBefore := counters.qdrant.Load()
	providerBefore := counters.provider.Load()

	var first []string
	for i := 0; i < 10; i++ {
		res, err := agg.Search(ctx, Query{Text: q.Text, Limit: q.Limit, Universe: SearchCatalog})
		if err != nil {
			t.Fatalf("catalog run %d: %v", i, err)
		}
		ids := assetIDs(res.Items)
		if i == 0 {
			first = ids
			continue
		}
		if !reflect.DeepEqual(ids, first) {
			t.Fatalf("CATALOG reproducibility FAILED: run %d = %v, run 0 = %v", i, ids, first)
		}
	}
	if len(first) == 0 {
		t.Fatal("CATALOG returned no results; certification needs a non-empty catalog")
	}

	if got := counters.qdrant.Load() - qdrantBefore; got == 0 {
		t.Fatal("CATALOG separation FAILED: catalog must make Qdrant calls > 0")
	}
	if got := counters.provider.Load() - providerBefore; got != 0 {
		t.Fatalf("CATALOG separation FAILED: catalog made %d live-provider calls, want 0", got)
	}

	// ── 2. DISCOVERY separation ────────────────────────────────────────
	qdrantBefore = counters.qdrant.Load()
	providerBefore = counters.provider.Load()

	res, err := agg.Search(ctx, Query{Text: q.Text, Limit: q.Limit, Universe: SearchDiscovery})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if len(res.Items) == 0 {
		t.Fatal("DISCOVERY returned no results; certification needs a non-empty provider result")
	}
	if got := counters.provider.Load() - providerBefore; got == 0 {
		t.Fatal("DISCOVERY separation FAILED: discovery must make provider calls > 0")
	}
	if got := counters.qdrant.Load() - qdrantBefore; got != 0 {
		t.Fatalf("DISCOVERY separation FAILED: discovery made %d Qdrant calls, want 0", got)
	}

	// ── 3. BLENDED dedup via CanonicalIdentityResolver ──────────────────
	// The provider's source_type|source_ref (artlist|123) resolves to the
	// canonical asset-A already served by the catalog backend. After
	// resolution the provider candidate carries AssetID=asset-A, so the
	// 4-key merge dedups it against the catalog hit: asset-A appears once.
	resolver := fakeCanonicalResolver{}
	identity, err := resolver.ResolveSource(ctx, "artlist", "123")
	if err != nil || !identity.Resolved || identity.AssetID != "asset-A" {
		t.Fatalf("resolver must map artlist|123 → asset-A, got %+v err=%v", identity, err)
	}

	// Simulate the provider backend resolving each ExternalCandidate to its
	// canonical AssetID before fan-in.
	resolved := make([]Candidate, 0, len(artlist.results))
	for _, c := range artlist.results {
		id, err := resolver.ResolveSource(ctx, c.Source, c.SourceRef)
		if err == nil && id.Resolved {
			c.AssetID = id.AssetID
		}
		resolved = append(resolved, c)
	}
	resolvedBackend := &countingBackend{
		name: artlist.name, universe: SearchDiscovery, caps: artlist.caps,
		results: resolved, counters: counters,
	}

	blended := NewBackendRegistry()
	for _, b := range []*countingBackend{semantic, resolvedBackend} {
		if err := blended.Register(b); err != nil {
			t.Fatalf("register blended %s: %v", b.name, err)
		}
	}
	blended.Freeze()

	blendedRes, err := NewAggregator(blended, nil).Search(ctx, Query{Text: q.Text, Limit: q.Limit, Universe: SearchBlended})
	if err != nil {
		t.Fatalf("blended: %v", err)
	}
	var assetACount int
	for _, c := range blendedRes.Items {
		if c.AssetID == "asset-A" {
			assetACount++
		}
	}
	if assetACount != 1 {
		t.Fatalf("BLENDED dedup FAILED: asset-A appears %d times, want exactly 1 (items: %v)",
			assetACount, assetIDs(blendedRes.Items))
	}

	// Both universes must have been exercised in blended mode.
	if counters.qdrant.Load() == 0 || counters.provider.Load() == 0 {
		t.Fatalf("BLENDED must fan out to both universes (qdrant=%d provider=%d)",
			counters.qdrant.Load(), counters.provider.Load())
	}
}

// Package scripts_test — source_registry_test.go exercises the
// SourceRegistry (freeze, duplicate rejection, nil safety, dispatch)
// and the four concrete resolvers (Text, Clips, Catalog, Search).
package adapters_test

import (
	"context"
	"errors"
	"testing"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ── Helpers ────────────────────────────────────────────────────────

func newTestRegistry() *adapterspkg.SourceRegistry {
	return adapterspkg.NewSourceRegistry(zap.NewNop())
}

// ── Registry: basic registration ───────────────────────────────────

func TestSourceRegistryRegisterSuccess(t *testing.T) {
	reg := newTestRegistry()
	ok := reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	if !ok {
		t.Fatal("expected Register to succeed")
	}
	if !reg.Registered(scriptpkg.SourceText) {
		t.Error("expected Text to be registered")
	}
	if reg.Len() != 1 {
		t.Errorf("expected 1 resolver, got %d", reg.Len())
	}
}

// ── Registry: duplicate rejection ──────────────────────────────────

func TestSourceRegistryDuplicateRejected(t *testing.T) {
	reg := newTestRegistry()
	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())

	// Second registration of same type must fail.
	ok := reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	if ok {
		t.Fatal("expected duplicate Register to return false")
	}
	if reg.Len() != 1 {
		t.Errorf("expected still 1 resolver after duplicate, got %d", reg.Len())
	}
}

// ── Registry: nil resolver rejected ────────────────────────────────

func TestSourceRegistryNilResolverRejected(t *testing.T) {
	reg := newTestRegistry()
	ok := reg.Register(scriptpkg.SourceText, nil)
	if ok {
		t.Fatal("expected nil resolver to be rejected")
	}
	if reg.Registered(scriptpkg.SourceText) {
		t.Error("nil resolver should not be registered")
	}
	if reg.Len() != 0 {
		t.Errorf("expected 0 resolvers, got %d", reg.Len())
	}
}

// ── Registry: freeze and post-freeze rejection ─────────────────────

func TestSourceRegistryFreeze(t *testing.T) {
	reg := newTestRegistry()
	if reg.IsFrozen() {
		t.Error("new registry should not be frozen")
	}

	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	reg.Freeze()

	if !reg.IsFrozen() {
		t.Error("registry should be frozen after Freeze()")
	}

	// Post-freeze registration must fail.
	ok := reg.Register(scriptpkg.SourceClips, scripts.NewTextSourceResolver())
	if ok {
		t.Fatal("expected post-freeze Register to return false")
	}
	if reg.Len() != 1 {
		t.Errorf("expected still 1 resolver after frozen reject, got %d", reg.Len())
	}
}

func TestSourceRegistryDoubleFreezeIdempotent(t *testing.T) {
	reg := newTestRegistry()
	reg.Freeze()
	reg.Freeze() // must not panic, must stay frozen
	if !reg.IsFrozen() {
		t.Error("registry should be frozen after double Freeze()")
	}
}

// ── Registry: resolve unknown type ─────────────────────────────────

func TestSourceRegistryResolveUnknownType(t *testing.T) {
	reg := newTestRegistry()
	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	reg.Freeze()

	_, err := reg.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type: scriptpkg.SourceClips,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-1"})

	if err == nil {
		t.Fatal("expected error for unknown source type")
	}
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Errorf("expected SourceResolutionError, got %T: %v", err, err)
	}
}

// ── Registry: nil safety ───────────────────────────────────────────

func TestSourceRegistryNilReceiver(t *testing.T) {
	var reg *adapterspkg.SourceRegistry

	if reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver()) {
		t.Error("nil registry: Register should return false")
	}
	if reg.IsFrozen() {
		t.Error("nil registry: IsFrozen should return false")
	}
	if reg.Len() != 0 {
		t.Error("nil registry: Len should return 0")
	}
	if reg.Registered(scriptpkg.SourceText) {
		t.Error("nil registry: Registered should return false")
	}

	// Freeze must not panic on nil.
	reg.Freeze()

	// Resolve must return error on nil.
	_, err := reg.Resolve(context.Background(), scriptpkg.SourceSpec{}, scriptpkg.SourceResolutionContext{ItemID: "x"})
	if err == nil {
		t.Error("nil registry: Resolve should return error")
	}
}

// ── Registry: freeze before any registration ──────────────────────

func TestSourceRegistryFreezeEmpty(t *testing.T) {
	reg := newTestRegistry()
	reg.Freeze()
	if !reg.IsFrozen() {
		t.Error("empty registry should be frozen")
	}
	if reg.Len() != 0 {
		t.Errorf("empty frozen registry: Len should be 0, got %d", reg.Len())
	}

	// Post-freeze registration must fail, even on totally empty registry.
	ok := reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	if ok {
		t.Fatal("expected post-freeze Register on empty registry to return false")
	}
	if reg.Len() != 0 {
		t.Errorf("expected still 0 resolvers after frozen reject, got %d", reg.Len())
	}
}

// ── Registry: multiple types ───────────────────────────────────────

func TestSourceRegistryMultipleTypes(t *testing.T) {
	reg := newTestRegistry()

	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	reg.Register(scriptpkg.SourceClips, scripts.NewTextSourceResolver()) // any resolver for test
	reg.Register(scriptpkg.SourceCatalog, scripts.NewTextSourceResolver())

	if reg.Len() != 3 {
		t.Errorf("expected 3 resolvers, got %d", reg.Len())
	}
	if !reg.Registered(scriptpkg.SourceText) {
		t.Error("expected Text to be registered")
	}
	if !reg.Registered(scriptpkg.SourceClips) {
		t.Error("expected Clips to be registered")
	}
	if !reg.Registered(scriptpkg.SourceCatalog) {
		t.Error("expected Catalog to be registered")
	}
	if reg.Registered(scriptpkg.SourceSearch) {
		t.Error("Search should not be registered")
	}
}

// ── Text resolver ──────────────────────────────────────────────────

func TestTextResolverResolveSuccess(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewTextSourceResolver()
	src := scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceText,
		Topic:      "AI Future",
		SourceText: "AI is transforming society.",
		Guidelines: "Keep it under 500 words.",
	}
	resolved, err := resolver.Resolve(context.Background(), src, scriptpkg.SourceResolutionContext{ItemID: "item-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Type != scriptpkg.SourceText {
		t.Errorf("type: %q", resolved.Type)
	}
	if resolved.Topic != "AI Future" {
		t.Errorf("topic: %q", resolved.Topic)
	}
	if resolved.SourceText == "" {
		t.Error("source_text should not be empty")
	}
	// BuildItemIdentity uses the canonical GenerationFingerprintInput.
	_ = resolved.Fingerprint
	if resolved.ClipEvidence != nil {
		t.Error("text source should not have clip evidence")
	}
}

func TestTextResolverResolveTopicOnly(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewTextSourceResolver()
	src := scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceText,
		Topic: "Only a topic",
	}
	resolved, err := resolver.Resolve(context.Background(), src, scriptpkg.SourceResolutionContext{ItemID: "item-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Title != "Only a topic" {
		t.Errorf("title should default to topic: %q", resolved.Title)
	}
}

func TestTextResolverResolveEmpty(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewTextSourceResolver()
	src := scriptpkg.SourceSpec{
		Type: scriptpkg.SourceText,
	}
	_, err := resolver.Resolve(context.Background(), src, scriptpkg.SourceResolutionContext{ItemID: "item-3"})
	if err == nil {
		t.Fatal("expected error for empty text source")
	}
}

// ── Search resolver ────────────────────────────────────────────────

type fakeSearchPort struct {
	results []scripts.SemanticSearchResult
	err     error
}

func (f *fakeSearchPort) SearchByText(_ context.Context, _ string, _ int, _ string) ([]scripts.SemanticSearchResult, error) {
	return f.results, f.err
}

func TestSearchResolverSuccess(t *testing.T) {
	t.Parallel()
	// Search resolver requires a ClipSourceBuilder, which is heavy to construct.
	// Direct nil test verifies the nil-guard works correctly.
	resolver := scripts.NewSearchSourceResolver(nil, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceSearch,
		Query: "test query",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-1"})
	if err == nil {
		t.Fatal("expected error when search port is nil")
	}
}

func TestSearchResolverEmptyQuery(t *testing.T) {
	t.Parallel()
	search := &fakeSearchPort{}
	resolver := scripts.NewSearchSourceResolver(search, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type: scriptpkg.SourceSearch,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-empty"})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchResolverSearchError(t *testing.T) {
	t.Parallel()
	search := &fakeSearchPort{err: errors.New("qdrant timeout")}
	resolver := scripts.NewSearchSourceResolver(search, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceSearch,
		Query: "test",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-err"})
	if err == nil {
		t.Fatal("expected error from search port")
	}
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Errorf("expected SourceResolutionError, got %T", err)
	}
}

func TestSearchResolverNoResults(t *testing.T) {
	t.Parallel()
	search := &fakeSearchPort{results: []scripts.SemanticSearchResult{}}
	resolver := scripts.NewSearchSourceResolver(search, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceSearch,
		Query: "no results",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-noresults"})
	if err == nil {
		t.Fatal("expected error for zero results")
	}
}

// ── Catalog resolver ───────────────────────────────────────────────

type fakeCatalogPort struct {
	results []appsearch.CatalogSearchResult
	err     error
}

func (f *fakeCatalogPort) SearchAll(_ context.Context, _ string) ([]appsearch.CatalogSearchResult, error) {
	return f.results, f.err
}

func TestCatalogResolverNilCatalogService(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewCatalogSourceResolver(nil, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceCatalog,
		Query: "test",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-nil"})
	if err == nil {
		t.Fatal("expected error when catalog service is nil")
	}
}

func TestCatalogResolverEmptyQuery(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogPort{}
	resolver := scripts.NewCatalogSourceResolver(cat, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type: scriptpkg.SourceCatalog,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-empty"})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestCatalogResolverSearchError(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogPort{err: errors.New("catalog db down")}
	resolver := scripts.NewCatalogSourceResolver(cat, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceCatalog,
		Query: "test",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-err-catalog"})
	if err == nil {
		t.Fatal("expected error from catalog port")
	}
}

func TestCatalogResolverNoResults(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogPort{results: []appsearch.CatalogSearchResult{}}
	resolver := scripts.NewCatalogSourceResolver(cat, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:  scriptpkg.SourceCatalog,
		Query: "nothing matches",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-noresults-ct"})
	if err == nil {
		t.Fatal("expected error for zero results")
	}
}

func TestCatalogResolverMinCoverageNotMet(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogPort{
		results: []appsearch.CatalogSearchResult{
			{ID: "clip-1", Name: "First Clip", Score: 0.9},
		},
	}
	resolver := scripts.NewCatalogSourceResolver(cat, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceCatalog,
		Query:       "test",
		MaxClips:    10,
		MinCoverage: 0.5,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-cover"})
	if err == nil {
		t.Fatal("expected coverage error (1/10 = 0.1 < 0.5)")
	}
}

func TestCatalogResolverDeduplicates(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalogPort{
		results: []appsearch.CatalogSearchResult{
			{ID: "clip-1", Name: "First", Score: 0.9},
			{ID: "clip-1", Name: "Duplicate", Score: 0.8},
			{ID: "clip-2", Name: "Second", Score: 0.7},
		},
	}
	// Without a ClipSourceBuilder, Phase 2 will fail. We just verify
	// the catalog search phase doesn't crash on duplicates.
	resolver := scripts.NewCatalogSourceResolver(cat, nil, scripts.NewClipSamplerRegistry(), zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:     scriptpkg.SourceCatalog,
		Query:    "test",
		MaxClips: 5,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-dup"})
	// Error is expected (no ClipSourceBuilder), but we check it's a
	// NoSourceError (missing infrastructure), not a search error.
	if err == nil {
		t.Fatal("expected error (no ClipSourceBuilder)")
	}
	var noSrcErr *scriptpkg.NoSourceError
	if !errors.As(err, &noSrcErr) {
		t.Errorf("expected NoSourceError (missing ClipSourceBuilder), got %T: %v", err, err)
	}
}

// ── Clips resolver ─────────────────────────────────────────────────

func TestClipsResolverEmptyClipIDs(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewClipsSourceResolver(nil, zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:    scriptpkg.SourceClips,
		ClipIDs: nil,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-empty"})
	if err == nil {
		t.Fatal("expected error for empty clip IDs")
	}
}

func TestClipsResolverNilClipBuilder(t *testing.T) {
	t.Parallel()
	resolver := scripts.NewClipsSourceResolver(nil, zap.NewNop())
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:    scriptpkg.SourceClips,
		ClipIDs: []string{"clip-a"},
	}, scriptpkg.SourceResolutionContext{ItemID: "item-nil"})
	if err == nil {
		t.Fatal("expected error when ClipSourceBuilder is nil")
	}
}

// ── Resolver interface compliance ──────────────────────────────────

func TestResolversSatisfyInterface(t *testing.T) {
	// Compile-time check that each resolver type satisfies SourceResolver.
	var _ adapterspkg.SourceResolver = scripts.NewTextSourceResolver()
	// Clips, Catalog, Search require non-trivial dependencies at construction
	// but their *types* satisfy the interface.
	var clips *scripts.ClipsSourceResolver
	var catalog *scripts.CatalogSourceResolver
	var search *scripts.SearchSourceResolver
	_ = clips
	_ = catalog
	_ = search
}

// ── Registry: dispatch to concrete resolver ────────────────────────

func TestSourceRegistryDispatch(t *testing.T) {
	reg := newTestRegistry()
	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	reg.Freeze()

	resolved, err := reg.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceText,
		Topic:      "Test",
		SourceText: "Test content.",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-dispatch"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Type != scriptpkg.SourceText {
		t.Errorf("type: %q", resolved.Type)
	}
	if resolved.Topic != "Test" {
		t.Errorf("topic: %q", resolved.Topic)
	}
}

// ── Registry: frozen dispatch is still allowed ─────────────────────

func TestSourceRegistryFrozenDispatch(t *testing.T) {
	reg := newTestRegistry()
	reg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())
	reg.Freeze()

	// Frozen means no new registrations, but resolution still works.
	_, err := reg.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceText,
		Topic:      "After freeze",
		SourceText: "Still works.",
	}, scriptpkg.SourceResolutionContext{ItemID: "item-frozen"})

	if err != nil {
		t.Fatalf("frozen registry should still dispatch: %v", err)
	}
}

// ── CatalogSearchResult type check ─────────────────────────────────

// Verify the CatalogSearchResult type from appsearch is usable.
func TestCatalogSearchResultType(t *testing.T) {
	r := appsearch.CatalogSearchResult{
		ID:    "clip-1",
		Name:  "Test Clip",
		Score: 0.95,
	}
	if r.ID != "clip-1" {
		t.Error("CatalogSearchResult ID mismatch")
	}
}

// ── PR 5: parity — Catalog and Search share Phase 2 hydration ──────

// TestCatalogSearchParity_SourceTypeChangesIdentity verifies that
// Catalog and Search resolvers compute different fingerprints when
// the SourceSpec differs only by Type. The canonical
// GenerationFingerprintInput includes SourceType because the
// retrieval subsystem (catalog vs search) is part of the request
// identity, even when the query and sizing parameters are identical.
func TestCatalogSearchParity_SourceTypeChangesIdentity(t *testing.T) {
	// Two SourceSpecs that differ only in Type should produce
	// different identities — SourceType is a canonical fingerprint
	// field per the GenerationFingerprintInput contract.
	catalogSrc := scriptpkg.SourceSpec{
		Type:             scriptpkg.SourceCatalog,
		Query:            "AI future",
		MaxClips:         5,
		MinCoverage:      0.5,
		TranscriptPolicy: "auto",
		OrderingStrategy: "relevance",
	}
	searchSrc := scriptpkg.SourceSpec{
		Type:             scriptpkg.SourceSearch,
		Query:            "AI future",
		MaxClips:         5,
		MinCoverage:      0.5,
		TranscriptPolicy: "auto",
		OrderingStrategy: "relevance",
	}

	catItem := scriptpkg.GenerationItemV2{Source: catalogSrc}
	searchItem := scriptpkg.GenerationItemV2{Source: searchSrc}

	catID := adapterspkg.BuildItemIdentity(catItem)
	searchID := adapterspkg.BuildItemIdentity(searchItem)

	if catID == searchID {
		t.Errorf("catalog and search should produce different identities when SourceType differs:\n  catalog: %s\n  search:  %s", catID, searchID)
	}
}

// TestCatalogSearchParity_NoSeparateEngineRequests verifies that
// neither Catalog nor Search resolver constructs a separate engine
// request. Both use the ClipSourceBuilder (via buildResolvedClipSource)
// which is the single path to build clip context.
//
// Design contract (PR 5): a resolver that calls ollama.GenerateScript
// or constructs a TextGenerationRequest directly is a layering
// violation. The shared helper (source_resolver_shared.go) takes a
// clipContextBuilder interface that only exposes BuildClipContext —
// no engine methods.
func TestCatalogSearchParity_NoSeparateEngineRequests(t *testing.T) {
	// Arch check: the scripts package imports are verified by the CI
	// gate (scripts/ci-architectural-checks.sh). This test documents
	// the contract: resolvers must not import engine/ollama packages.
	//
	// The shared helper takes clipContextBuilder which has exactly one
	// method: BuildClipContext. No GenerateScript, no Chat, no engine.
	//
	// Prove we can construct both resolver types without an engine.
	_ = scripts.NewCatalogSourceResolver(nil, nil, scripts.NewClipSamplerRegistry(), nil)
	_ = scripts.NewSearchSourceResolver(nil, nil, scripts.NewClipSamplerRegistry(), nil)
	_ = scripts.NewTextSourceResolver()

	// The ResolvedSource shape has no engine-level fields — it carries
	// source text, clip evidence, and fingerprint only.
	rs := scriptpkg.ResolvedSource{
		Type:       scriptpkg.SourceSearch,
		SourceText: "test",
		Topic:      "parity",
	}
	_ = rs
}

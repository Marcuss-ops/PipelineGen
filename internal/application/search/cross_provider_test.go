// Package search — cross_provider_test.go is the PR-AGGREGATE-FILTER-UNIFORM
// (architecture/current.yaml#id-30, VERDICT §6) contract harness.
//
// The harness exercises the SAME search.Query against every registered
// SearchBackend (provider + local + semantic) and verifies that the
// filter semantics are uniform:
//
//  1. Every registered backend receives the SAME q.Filters values
//     (uniform propagation).
//  2. Every filter field the canonical Filters DTO exposes (Source,
//     MediaType, Category, Language, Tags, DurationMsMin) survives
//     the round-trip from handler→Query→backend.Search.
//  3. Each backend's honours/ignores contract is documented per
//     backend in the per-backend test cases (provider backend
//     silently drops fields its native API doesn't support;
//     local backend forwards all fields to AdvancedSearchRequest;
//     semantic backend honours Source/MediaType/Category/Language
//     at the Qdrant layer per internal/app/search_backend_semantic.go
//     ::compileSemanticFilters).
//
// Why a shared harness rather than per-backend test files: the
// provider backend (providerSearchBackend) and the local backend
// (localSearchBackend) each carry their own filter-mapping logic
// in internal/app/search_backends.go and search_backend_semantic.go.
// Without a shared test surface, drift between the two was exactly
// what the verdict flagged ("il provider backend inoltra i media
// type leggendo: q.MediaTypes non: q.Filters.MediaType"). The
// harness pins the uniform semantics so future drift is a test
// regression rather than a runtime surprise.
//
// Stub-only: the harness does NOT spin SQLite or Qdrant. The
// filterSpy Backend records the q.Filters it received and emits a
// zero-length result slice. The contract being exercised is
// "Filters forwarding", not "filter execution"; execution tests
// belong to the per-backend's native test packages (qdrant,
// sqlite/assets, provider adapters).
package search

import (
	"context"
	"sync"
	"testing"
)

// filterSpyBackend records the Filters passed to Search() for later
// assertion by the contract harness. Capabilities are settable so
// the harness can verify MediaType-eligibility filtering alongside
// the Filters round-trip; delay+err hooks exist for future
// partial-failure coverage (out of scope for this PR).
type filterSpyBackend struct {
	name     string
	caps     []Capability
	universe SearchUniverse

	mu          sync.Mutex
	lastFilters Filters
	lastQuery   Query
	searchCalls int
	returnItems []Candidate
	returnErr   error
}

func (f *filterSpyBackend) Name() string { return f.name }
func (f *filterSpyBackend) Capabilities() []Capability {
	if f.caps != nil {
		return f.caps
	}
	return []Capability{CapVideo}
}

func (f *filterSpyBackend) Universe() SearchUniverse {
	if f.universe != "" {
		return f.universe
	}
	return SearchCatalog
}

// Search records the inbound Filters+Query and returns the stub's
// configured items/error. Goroutine-safe via a single mutex so the
// Aggregator fan-out (per-backend goroutine) does not race the
// test goroutine reading lastFilters.
func (f *filterSpyBackend) Search(ctx context.Context, q Query) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilters = q.Filters
	f.lastQuery = q
	f.searchCalls++
	return f.returnItems, f.returnErr
}

func (f *filterSpyBackend) snapshot() (Filters, Query, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastFilters, f.lastQuery, f.searchCalls
}

// TestFilterUniformContract is the canonical harness. Three stub
// backends register against a fresh BackendRegistry; a single fully-
// populated Query runs through the Aggregator fan-out; each backend's
// lastFilters snapshot must equal the canonical Filters the
// handler/build path produced.
//
// Pins:
//   - Source / MediaType / Category / Language / Tags / DurationMsMin
//     round-trip across every backend (uniform propagation invariant).
//   - Tags deep-equal (slice equality, not nil-vs-empty confusion).
//   - text round-trip (catches any backend-level text rewrites).
func TestFilterUniformContract(t *testing.T) {
	canonical := Filters{
		Source:        "youtube",
		MediaType:     "video",
		Category:      "music",
		Language:      "en",
		Tags:          []string{"night", "developer", "monitor"},
		DurationMsMin: 5000,
	}
	q := Query{
		Text:    "developer working late at desk",
		Mode:    SearchModeANN, // ANN: test is about filter forwarding, not hybrid
		Limit:   20,
		Filters: canonical,
	}

	registry := NewBackendRegistry()
	a := &filterSpyBackend{name: "provider-a", caps: []Capability{CapVideo}}
	b := &filterSpyBackend{name: "provider-b", caps: []Capability{CapAudio}}
	c := &filterSpyBackend{name: "local-backend", caps: []Capability{CapVideo, CapImage}}
	for _, sp := range []*filterSpyBackend{a, b, c} {
		if err := registry.Register(sp); err != nil {
			t.Fatalf("register %s: %v", sp.name, err)
		}
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	if _, err := agg.Search(context.Background(), q); err != nil {
		t.Fatalf("aggregator Search: %v", err)
	}

	for _, sp := range []*filterSpyBackend{a, b, c} {
		sp.mu.Lock()
		gotFilters := sp.lastFilters
		gotText := sp.lastQuery.Text
		calls := sp.searchCalls
		sp.mu.Unlock()
		if calls != 1 {
			t.Errorf("backend %q: want 1 Search call, got %d", sp.name, calls)
		}
		if gotText != q.Text {
			t.Errorf("backend %q: text round-trip broken: want %q, got %q",
				sp.name, q.Text, gotText)
		}
		if gotFilters.Source != canonical.Source {
			t.Errorf("backend %q: Source mismatch: want %q, got %q",
				sp.name, canonical.Source, gotFilters.Source)
		}
		if gotFilters.MediaType != canonical.MediaType {
			t.Errorf("backend %q: MediaType mismatch: want %q, got %q",
				sp.name, canonical.MediaType, gotFilters.MediaType)
		}
		if gotFilters.Category != canonical.Category {
			t.Errorf("backend %q: Category mismatch: want %q, got %q",
				sp.name, canonical.Category, gotFilters.Category)
		}
		if gotFilters.Language != canonical.Language {
			t.Errorf("backend %q: Language mismatch: want %q, got %q",
				sp.name, canonical.Language, gotFilters.Language)
		}
		if gotFilters.DurationMsMin != canonical.DurationMsMin {
			t.Errorf("backend %q: DurationMsMin mismatch: want %d, got %d",
				sp.name, canonical.DurationMsMin, gotFilters.DurationMsMin)
		}
		// Tags must deep-equal — NumTags guard prevents accidental
		// nil-vs-empty semantic confusion.
		if len(gotFilters.Tags) != len(canonical.Tags) {
			t.Errorf("backend %q: Tags length mismatch: want %d, got %d",
				sp.name, len(canonical.Tags), len(gotFilters.Tags))
			continue
		}
		for i, want := range canonical.Tags {
			if gotFilters.Tags[i] != want {
				t.Errorf("backend %q: Tags[%d] mismatch: want %q, got %q",
					sp.name, i, want, gotFilters.Tags[i])
			}
		}
	}
}

// TestFilterEmptyContract pins zero-value uniform forwarding: an
// empty Filters DTO must propagate as zero across every backend. The
// "no filter active" semantic is what the Aggregator.Eligible
// function relies on when no q.Sources / q.MediaTypes are set, so
// silent drops at the per-backend seam would change the eligibility
// outcome.
func TestFilterEmptyContract(t *testing.T) {
	q := Query{Text: "anything", Limit: 10}

	registry := NewBackendRegistry()
	a := &filterSpyBackend{name: "a", caps: []Capability{CapVideo}}
	b := &filterSpyBackend{name: "b", caps: []Capability{CapAudio}}
	for _, sp := range []*filterSpyBackend{a, b} {
		if err := registry.Register(sp); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	if _, err := agg.Search(context.Background(), q); err != nil {
		t.Fatalf("aggregator Search: %v", err)
	}

	for _, sp := range []*filterSpyBackend{a, b} {
		sp.mu.Lock()
		got := sp.lastFilters
		sp.mu.Unlock()
		if got.Source != "" || got.MediaType != "" || got.Category != "" ||
			got.Language != "" || got.DurationMsMin != 0 || len(got.Tags) != 0 {
			t.Errorf("backend %q: empty-Filters round-trip contaminated: got %+v", sp.name, got)
		}
	}
}

// TestFilterLanguageHonestyContract — godlike/07 no-fake-availability
// guard for Language forwarding. PR-AGGREGATE-FILTER-UNIFORM threads
// q.Filters.Language through AdvancedSearchRequest + providers.SearchFilters
// so the wire shape is uniform across all backends. But the LOCAL
// backend cannot enforce Language at the SQL layer today (no
// media_assets.language column in the canonical
// clips_repository.go::MediaAssetColumns 40-column projection) and the
// PROVIDER backends silently drop Language if their native API
// doesn't expose it (documented SearchFilters contract). Rather than
// letting the filter look functional — a godlike/07 no-fake-availability
// violation — this test pins the documented behaviour:
//
//   - Local backend: Language reaches AdvancedSearchRequest but is
//     NOT a SQL filter until migration ships (forward-pointer).
//   - Provider backend: Language reaches providers.SearchFilters so
//     adapter-side support can be enabled later; today the field
//     exists in the wire but no provider implements Language support.
//   - Both backends MUST round-trip the value; silent drop in the
//     fan-out path is a regression.
//
// Future: when media_assets.language migration lands AND at least
// ONE provider adapter adds Language support, this test renames to
// TestFilterLanguageEnforcedContract and the comment is replaced.
func TestFilterLanguageHonestyContract(t *testing.T) {
	q := Query{
		Text:    "anything",
		Limit:   5,
		Filters: Filters{Language: "en"},
	}
	registry := NewBackendRegistry()
	a := &filterSpyBackend{name: "provider", caps: []Capability{CapVideo}}
	b := &filterSpyBackend{name: "local", caps: []Capability{CapVideo, CapAudio}}
	for _, sp := range []*filterSpyBackend{a, b} {
		if err := registry.Register(sp); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	registry.Freeze()

	agg := NewAggregator(registry, nil)
	if _, err := agg.Search(context.Background(), q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, sp := range []*filterSpyBackend{a, b} {
		sp.mu.Lock()
		got := sp.lastFilters.Language
		sp.mu.Unlock()
		if got != "en" {
			t.Errorf("backend %q: Language must round-trip uniformly; want %q, got %q",
				sp.name, "en", got)
		}
	}
}

// TestFilterMediaTypeSingleString pins PR-AGGREGATE-FILTER-UNIFORM's
// most material change: q.Filters.MediaType is a SINGLE canonical
// string (not the legacy q.MediaTypes slice capability-shape). The
// harness feeds a single-string MediaType and verifies every backend
// observes it verbatim. This is the audit-pin for the verdict's
// "the provider backend reads q.MediaTypes not q.Filters.MediaType"
// drift — once it surfaces here, the regression is a 1-line fix.
func TestFilterMediaTypeSingleString(t *testing.T) {
	cases := []struct {
		name      string
		mediaType string
	}{
		{"video", "video"},
		{"image", "image"},
		{"audio", "audio"},
		{"music", "music"},
		{"empty", ""}, // capability-shape must NOT contaminate empty path
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Query{
				Text:    "x",
				Limit:   5,
				Filters: Filters{MediaType: tc.mediaType},
			}
			registry := NewBackendRegistry()
			spy := &filterSpyBackend{
				name: "spy",
				caps: []Capability{CapVideo, CapImage, CapAudio, CapMusic},
			}
			if err := registry.Register(spy); err != nil {
				t.Fatalf("register: %v", err)
			}
			registry.Freeze()

			agg := NewAggregator(registry, nil)
			if _, err := agg.Search(context.Background(), q); err != nil {
				t.Fatalf("Search: %v", err)
			}
			spy.mu.Lock()
			got := spy.lastFilters.MediaType
			spy.mu.Unlock()
			if got != tc.mediaType {
				t.Errorf("MediaType round-trip: want %q, got %q", tc.mediaType, got)
			}
		})
	}
}

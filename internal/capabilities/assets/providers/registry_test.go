package assets

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// fakeSearchProvider implements only SearchProvider (no Fetch).
// Used by tests that exercise the search side of the registry.
type fakeSearchProvider struct {
	name string
	caps []Capability
}

func (p *fakeSearchProvider) Name() string               { return p.name }
func (p *fakeSearchProvider) Capabilities() []Capability { return p.caps }
func (p *fakeSearchProvider) Search(_ context.Context, _ SearchRequest) (SearchResult, error) {
	return SearchResult{}, nil
}

// fakeFetchProvider implements only FetchProvider (no Search).
// Used to verify the registry holds fetch-only providers
// independently of search-only ones.
type fakeFetchProvider struct {
	name string
	caps []Capability
}

func (p *fakeFetchProvider) Name() string               { return p.name }
func (p *fakeFetchProvider) Capabilities() []Capability { return p.caps }
func (p *fakeFetchProvider) Fetch(_ context.Context, _ FetchRequest) (*FetchedAsset, error) {
	return nil, nil
}

// fakeFullProvider implements BOTH SearchProvider AND FetchProvider.
// Used to verify the registry treats cross-capability providers
// correctly under both ByCapability filters.
type fakeFullProvider struct {
	name string
	caps []Capability
}

func (p *fakeFullProvider) Name() string               { return p.name }
func (p *fakeFullProvider) Capabilities() []Capability { return p.caps }
func (p *fakeFullProvider) Search(_ context.Context, _ SearchRequest) (SearchResult, error) {
	return SearchResult{}, nil
}
func (p *fakeFullProvider) Fetch(_ context.Context, _ FetchRequest) (*FetchedAsset, error) {
	return nil, nil
}

// Compile-time assertions — catches interface drift at build time.
var (
	_ SearchProvider = (*fakeSearchProvider)(nil)
	_ FetchProvider  = (*fakeFetchProvider)(nil)
	_ SearchProvider = (*fakeFullProvider)(nil)
	_ FetchProvider  = (*fakeFullProvider)(nil)
	_ Provider       = (*fakeSearchProvider)(nil)
	_ Provider       = (*fakeFetchProvider)(nil)
	_ Provider       = (*fakeFullProvider)(nil)
)

// ── Register / Freeze ────────────────────────────────────────────

func TestRegister_AllowsDistinctProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeSearchProvider{name: "a", caps: []Capability{CapabilitySearch}}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&fakeSearchProvider{name: "b", caps: []Capability{CapabilitySearch}}); err != nil {
		t.Fatalf("second register: %v", err)
	}
	got, ok := r.Get("a")
	if !ok {
		t.Fatal(`Get("a") returned false`)
	}
	if got.Name() != "a" {
		t.Fatalf("Get: want name=a, got %q", got.Name())
	}
}

func TestRegister_RejectsNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); !errors.Is(err, ErrNilProvider) {
		t.Fatalf("want ErrNilProvider, got %v", err)
	}
}

func TestRegister_RejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeSearchProvider{name: ""}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("want ErrEmptyName, got %v", err)
	}
}

func TestRegister_EmptyNameRejectedEvenAfterFreeze(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	if err := r.Register(&fakeSearchProvider{name: ""}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("want ErrEmptyName (precedence over ErrFrozen), got %v", err)
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeSearchProvider{name: "a"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := r.Register(&fakeSearchProvider{name: "a"})
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("want ErrAlreadyRegistered, got %v", err)
	}
}

func TestFreeze_BlocksFurtherRegister(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	err := r.Register(&fakeSearchProvider{name: "z"})
	if !errors.Is(err, ErrFrozen) {
		t.Fatalf("want ErrFrozen, got %v", err)
	}
}

func TestFreeze_Idempotent(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	r.Freeze()
	r.Freeze()
	if !r.IsFrozen() {
		t.Fatal("IsFrozen should be true after multiple Freeze() calls")
	}
}

// ── ByCapability + capability split ──────────────────────────────

// TestByCapability_Filters exercises the Search/Fetch split: a
// search-only provider must appear under CapabilitySearch but NOT
// under CapabilityFetch, a fetch-only provider the inverse, and a
// full provider must appear under both.
func TestByCapability_Filters(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterSearch(&fakeSearchProvider{name: "a", caps: []Capability{CapabilitySearch}}); err != nil {
		t.Fatalf("register search-only a: %v", err)
	}
	if err := r.RegisterFetch(&fakeFetchProvider{name: "b", caps: []Capability{CapabilityFetch}}); err != nil {
		t.Fatalf("register fetch-only b: %v", err)
	}
	if err := r.Register(&fakeFullProvider{name: "c", caps: []Capability{CapabilitySearch, CapabilityFetch}}); err != nil {
		t.Fatalf("register full c: %v", err)
	}
	r.Freeze()

	search := r.ByCapability(CapabilitySearch)
	if len(search) != 2 {
		t.Fatalf("want 2 providers with Search, got %d (%v)", len(search), namesOf(search))
	}
	if search[0].Name() != "a" || search[1].Name() != "c" {
		t.Fatalf("expected search order [a, c], got %v", namesOf(search))
	}

	fetch := r.ByCapability(CapabilityFetch)
	if len(fetch) != 2 {
		t.Fatalf("want 2 providers with Fetch, got %d (%v)", len(fetch), namesOf(fetch))
	}
	if fetch[0].Name() != "b" || fetch[1].Name() != "c" {
		t.Fatalf("expected fetch order [b, c], got %v", namesOf(fetch))
	}
}

func TestByCapability_DeterministicOrder(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeSearchProvider{name: "z", caps: []Capability{CapabilitySearch}})
	_ = r.Register(&fakeSearchProvider{name: "a", caps: []Capability{CapabilitySearch}})
	_ = r.Register(&fakeSearchProvider{name: "m", caps: []Capability{CapabilitySearch}})
	r.Freeze()

	got := r.ByCapability(CapabilitySearch)
	want := []string{"a", "m", "z"}
	if len(got) != len(want) {
		t.Fatalf("want %d, got %d", len(want), len(got))
	}
	for i, p := range got {
		if p.Name() != want[i] {
			t.Fatalf("by-cap[%d] = %q, want %q", i, p.Name(), want[i])
		}
	}
}

func TestAll_AfterFreeze(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeSearchProvider{name: "a"})
	_ = r.Register(&fakeSearchProvider{name: "b"})
	r.Freeze()
	if got := len(r.All()); got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

func TestAll_DeterministicOrder(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeSearchProvider{name: "z"})
	_ = r.Register(&fakeSearchProvider{name: "a"})
	_ = r.Register(&fakeSearchProvider{name: "m"})
	r.Freeze()

	got := r.All()
	want := []string{"a", "m", "z"}
	if len(got) != len(want) {
		t.Fatalf("want %d, got %d", len(want), len(got))
	}
	for i, p := range got {
		if p.Name() != want[i] {
			t.Fatalf("all[%d] = %q, want %q", i, p.Name(), want[i])
		}
	}
}

func TestGet_MissingReturnsFalse(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeSearchProvider{name: "a"})
	if _, ok := r.Get("absent"); ok {
		t.Fatal(`Get("absent") should return false`)
	}
}

// ── Typed helpers ────────────────────────────────────────────────

func TestRegisterSearch_AcceptsSearchProvider(t *testing.T) {
	r := NewRegistry()
	full := &fakeFullProvider{name: "x", caps: []Capability{CapabilitySearch, CapabilityFetch}}
	if err := r.RegisterSearch(full); err != nil {
		t.Fatalf("RegisterSearch on a full provider: %v", err)
	}
	got, ok := r.Get("x")
	if !ok {
		t.Fatal(`Get("x") returned false`)
	}
	if _, ok := got.(FetchProvider); !ok {
		t.Error("full provider must still satisfy FetchProvider after RegisterSearch")
	}
	if _, ok := got.(SearchProvider); !ok {
		t.Error("full provider must still satisfy SearchProvider after RegisterSearch")
	}
}

func TestRegisterFetch_AcceptsFetchProvider(t *testing.T) {
	r := NewRegistry()
	full := &fakeFullProvider{name: "x", caps: []Capability{CapabilityFetch}}
	if err := r.RegisterFetch(full); err != nil {
		t.Fatalf("RegisterFetch on a fetch provider: %v", err)
	}
	got, ok := r.Get("x")
	if !ok {
		t.Fatal(`Get("x") returned false`)
	}
	if _, ok := got.(FetchProvider); !ok {
		t.Error("full provider must still satisfy FetchProvider after RegisterFetch")
	}
}

// ── Interface segregation guarantee ──────────────────────────────

// TestSearchOnly_DoesNotImplementFetchProvider enforces the
// interface segregation at runtime: a type that only declares
// Search MUST NOT satisfy FetchProvider, neither via the implicit
// interface satisfaction nor via the method set.
func TestSearchOnly_DoesNotImplementFetchProvider(t *testing.T) {
	var s SearchProvider = (*fakeSearchProvider)(nil)
	if _, ok := s.(FetchProvider); ok {
		t.Fatal("fakeSearchProvider must NOT satisfy FetchProvider")
	}

	rt := reflect.TypeOf((*fakeSearchProvider)(nil))
	if _, found := rt.MethodByName("Fetch"); found {
		t.Errorf("fakeSearchProvider should not declare Fetch method, but it's in the method set")
	}
}

func TestFetchOnly_DoesNotImplementSearchProvider(t *testing.T) {
	var f FetchProvider = (*fakeFetchProvider)(nil)
	if _, ok := f.(SearchProvider); ok {
		t.Fatal("fakeFetchProvider must NOT satisfy SearchProvider")
	}

	rt := reflect.TypeOf((*fakeFetchProvider)(nil))
	if _, found := rt.MethodByName("Search"); found {
		t.Errorf("fakeFetchProvider should not declare Search method, but it's in the method set")
	}
}

// ── Concurrency + typed nil ──────────────────────────────────────

// TestRegister_ConcurrentSafe runs under -race: 26 goroutines each
// register a provider with a unique name (a..z). All registrations
// must succeed and All() must contain exactly 26 entries.
func TestRegister_ConcurrentSafe(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 26; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i%26))
			if err := r.Register(&fakeSearchProvider{name: name}); err != nil {
				t.Errorf("concurrent register(%s): %v", name, err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(r.All()); got != 26 {
		t.Fatalf("expected 26 unique providers registered, got %d", got)
	}
}

// TestRegister_RejectsTypedNil covers the regression where
// `var p Provider = (*Adapter)(nil)` produces a non-nil interface
// value that holds a nil pointer. Without the reflect-based
// detector in Register, p.Name() would panic.
func TestRegister_RejectsTypedNil(t *testing.T) {
	r := NewRegistry()
	var typedNil *fakeSearchProvider // nil concrete value
	var iface Provider = typedNil
	if err := r.Register(iface); !errors.Is(err, ErrNilProvider) {
		t.Fatalf("want ErrNilProvider for typed-nil, got %v", err)
	}
}

func namesOf(ps []Provider) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

package providers

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a minimal Provider implementation for the
// registry tests. Search and Fetch return errors because these
// tests only exercise the registry, not the wire contract.
type fakeProvider struct {
	name string
	caps []Capability
}

func (p *fakeProvider) Name() string                       { return p.name }
func (p *fakeProvider) Capabilities() []Capability         { return p.caps }
func (p *fakeProvider) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	return nil, errors.New("fakeProvider.Search not used in registry tests")
}
func (p *fakeProvider) Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error) {
	return nil, errors.New("fakeProvider.Fetch not used in registry tests")
}

func TestRegister_AllowsDistinctProviders(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: "a", caps: []Capability{CapabilitySearch}}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "b", caps: []Capability{CapabilitySearch}}); err != nil {
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

func TestRegister_RejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: "a"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := r.Register(&fakeProvider{name: "a"})
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("want ErrAlreadyRegistered, got %v", err)
	}
}

func TestFreeze_BlocksFurtherRegister(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	err := r.Register(&fakeProvider{name: "z"})
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

func TestByCapability_Filters(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: "a", caps: []Capability{CapabilitySearch}}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "b", caps: []Capability{CapabilityFetch}}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "c", caps: []Capability{CapabilitySearch, CapabilityFetch}}); err != nil {
		t.Fatalf("register c: %v", err)
	}
	r.Freeze()

	got := r.ByCapability(CapabilitySearch)
	if len(got) != 2 {
		t.Fatalf("want 2 providers with Search, got %d", len(got))
	}
	names := map[string]bool{got[0].Name(): true, got[1].Name(): true}
	if !names["a"] || !names["c"] {
		t.Fatalf("expected providers [a, c], got %v", names)
	}
}

func TestAll_AfterFreeze(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeProvider{name: "a"})
	_ = r.Register(&fakeProvider{name: "b"})
	r.Freeze()
	if got := len(r.All()); got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

func TestGet_MissingReturnsFalse(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeProvider{name: "a"})
	if _, ok := r.Get("absent"); ok {
		t.Fatal(`Get("absent") should return false`)
	}
}

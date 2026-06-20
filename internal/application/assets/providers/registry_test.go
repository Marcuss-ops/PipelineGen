package providers

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeProvider is a minimal Provider implementation for the
// registry tests. Search and Fetch return nil (no error) because
// these tests only exercise the registry, not the wire contract.
type fakeProvider struct {
	name string
	caps []Capability
}

func (p *fakeProvider) Name() string               { return p.name }
func (p *fakeProvider) Capabilities() []Capability { return p.caps }
func (p *fakeProvider) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	return nil, nil
}
func (p *fakeProvider) Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error) {
	return nil, nil
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

func TestRegister_RejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{name: ""}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("want ErrEmptyName, got %v", err)
	}
}

func TestRegister_EmptyNameRejectedEvenAfterFreeze(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	if err := r.Register(&fakeProvider{name: ""}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("want ErrEmptyName (precedence over ErrFrozen), got %v", err)
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
	// Deterministic order: by name (a, c).
	if got[0].Name() != "a" || got[1].Name() != "c" {
		t.Fatalf("expected order [a, c], got [%s, %s]", got[0].Name(), got[1].Name())
	}
}

func TestByCapability_DeterministicOrder(t *testing.T) {
	r := NewRegistry()
	// Register out of order on purpose; ByCapability should sort.
	_ = r.Register(&fakeProvider{name: "z", caps: []Capability{CapabilitySearch}})
	_ = r.Register(&fakeProvider{name: "a", caps: []Capability{CapabilitySearch}})
	_ = r.Register(&fakeProvider{name: "m", caps: []Capability{CapabilitySearch}})
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
	_ = r.Register(&fakeProvider{name: "a"})
	_ = r.Register(&fakeProvider{name: "b"})
	r.Freeze()
	if got := len(r.All()); got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

func TestAll_DeterministicOrder(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&fakeProvider{name: "z"})
	_ = r.Register(&fakeProvider{name: "a"})
	_ = r.Register(&fakeProvider{name: "m"})
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
	_ = r.Register(&fakeProvider{name: "a"})
	if _, ok := r.Get("absent"); ok {
		t.Fatal(`Get("absent") should return false`)
	}
}

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
			if err := r.Register(&fakeProvider{name: name}); err != nil {
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
// value that holds a nil pointer. Without the reflect-based detector
// in Register, p.Name() would panic.
func TestRegister_RejectsTypedNil(t *testing.T) {
	r := NewRegistry()
	var typedNil *fakeProvider // nil concrete value
	var iface Provider = typedNil
	if err := r.Register(iface); !errors.Is(err, ErrNilProvider) {
		t.Fatalf("want ErrNilProvider for typed-nil, got %v", err)
	}
}

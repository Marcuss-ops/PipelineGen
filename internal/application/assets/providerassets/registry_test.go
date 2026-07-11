package providerassets

import (
	"context"
	"errors"
	"testing"
)

type fakeAdapter struct {
	name   string
	assets []ProviderAsset
	err    error
}

func (f *fakeAdapter) Name() string { return f.name }

func (f *fakeAdapter) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if f.err != nil {
		return SearchResult{}, f.err
	}
	return SearchResult{Assets: f.assets}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	adapter := &fakeAdapter{name: "artlist"}
	if err := r.Register(adapter); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	got, err := r.Get("artlist")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Name() != "artlist" {
		t.Fatalf("expected artlist, got %s", got.Name())
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); !errors.Is(err, ErrNilAdapter) {
		t.Fatalf("expected ErrNilAdapter, got %v", err)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "artlist"})
	if err := r.Register(&fakeAdapter{name: "artlist"}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("expected ErrAlreadyRegistered, got %v", err)
	}
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeAdapter{name: ""}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("expected ErrEmptyName, got %v", err)
	}
}

func TestRegistry_Freeze(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "artlist"})
	r.Freeze()
	if !r.IsFrozen() {
		t.Fatal("expected registry to be frozen")
	}
	if err := r.Register(&fakeAdapter{name: "pexels"}); err == nil {
		t.Fatal("expected error when registering to frozen registry")
	}
}

func TestRegistry_AllAndNames(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "pexels"})
	r.Register(&fakeAdapter{name: "artlist"})

	names := r.Names()
	if len(names) != 2 || names[0] != "artlist" || names[1] != "pexels" {
		t.Fatalf("unexpected names: %v", names)
	}
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 adapters, got %d", len(all))
	}
}

func TestRegistry_Search(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "artlist", assets: []ProviderAsset{{ID: "1"}}})

	res, err := r.Search(context.Background(), "artlist", SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Assets) != 1 || res.Assets[0].ID != "1" {
		t.Fatalf("unexpected assets: %v", res.Assets)
	}
}

func TestRegistry_SearchNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Search(context.Background(), "missing", SearchRequest{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegistry_SearchAll(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "artlist", assets: []ProviderAsset{{ID: "1"}}})
	r.Register(&fakeAdapter{name: "pexels", assets: []ProviderAsset{{ID: "2"}}})

	res, errs := r.SearchAll(context.Background(), SearchRequest{Query: "test"})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(res.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(res.Assets))
	}
}

func TestRegistry_SearchAllPartialError(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeAdapter{name: "artlist", assets: []ProviderAsset{{ID: "1"}}})
	r.Register(&fakeAdapter{name: "pexels", err: errors.New("boom")})

	res, errs := r.SearchAll(context.Background(), SearchRequest{Query: "test"})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if len(res.Assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(res.Assets))
	}
}

func TestRegistry_SearchAllNoAdapters(t *testing.T) {
	r := NewRegistry()
	res, errs := r.SearchAll(context.Background(), SearchRequest{Query: "test"})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(res.Assets) != 0 {
		t.Fatalf("expected no assets, got %d", len(res.Assets))
	}
}

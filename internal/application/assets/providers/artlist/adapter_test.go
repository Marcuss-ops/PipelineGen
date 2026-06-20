package artlist

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

// ── Compile-time + capability checks ──────────────────────────────

func TestAdapter_Name(t *testing.T) {
	// Adapter with nil src still reports its stable identity.
	a := &Adapter{}
	if got := a.Name(); got != "artlist" {
		t.Fatalf("Name()=%q, want \"artlist\"", got)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a := &Adapter{}
	caps := a.Capabilities()
	if !slices.Contains(caps, providers.CapabilitySearch) {
		t.Errorf("Capabilities() missing CapabilitySearch: %v", caps)
	}
	if !slices.Contains(caps, providers.CapabilityVideo) {
		t.Errorf("Capabilities() missing CapabilityVideo: %v", caps)
	}
	if !slices.Contains(caps, providers.CapabilityMusic) {
		t.Errorf("Capabilities() missing CapabilityMusic: %v", caps)
	}
	if slices.Contains(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() must NOT declare CapabilityFetch (no public fetch binary path): %v", caps)
	}
}

// Adapter must NOT implement FetchProvider — interface segregation
// guarantee (Agent 3 contract cleanup).
func TestAdapter_DoesNotImplementFetchProvider(t *testing.T) {
	var sp providers.SearchProvider = (*Adapter)(nil)
	if _, ok := sp.(providers.FetchProvider); ok {
		t.Fatal("artlist Adapter must NOT satisfy FetchProvider")
	}
}

// ── Nil-source handling ──────────────────────────────────────────

// TestSearch_NilSource ensures Search fails cleanly when no artlist
// service was injected at composition time. Behavioural coverage
// (with a fake searcher) is exercised by registry_test.go via the
// provider contract — the underlying *sources/artlist.Service is a
// concrete pointer and requires a private searcher interface for
// tractable in-process mocking. That refactor is deferred to
// follow the pattern of youtube/adapter.go (see its searcher).
func TestSearch_NilSource(t *testing.T) {
	a := &Adapter{} // src == nil
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired, got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates, got %v", res.Candidates)
	}
	if res.NextPageToken != "" {
		t.Errorf("expected empty NextPageToken, got %q", res.NextPageToken)
	}
}

func TestNewAdapter_NilService_SearchReturnsErrSourceNotWired(t *testing.T) {
	a := NewAdapter(nil)
	if a.Name() != "artlist" {
		t.Errorf("Name mismatch: got %q", a.Name())
	}
	if _, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"}); !errors.Is(err, ErrSourceNotWired) {
		t.Errorf("expected ErrSourceNotWired, got %v", err)
	}
}

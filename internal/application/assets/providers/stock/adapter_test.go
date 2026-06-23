package stock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	stock "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// Compile-time interface guard: catches interface drift at build time.
// The Adapter intentionally does NOT implement SearchProvider — Stock
// is fetch-only by design (see adapter.go package doc).
var _ providers.FetchProvider = (*stock.Adapter)(nil)

// fakeRunner is a minimal stub of stockpipeline.stockRunner. It captures the
// most-recent RunInput and returns canned outputs so unit tests can
// verify dispatch + happy-path mapping without standing up a real
// *stockpipeline.Service (which carries a heavy Drive+Jobs+AssetIndex
// dependency chain).
type fakeRunner struct {
	lastInput *stockpipeline.RunInput
	result    *stockpipeline.PipelineResult
	err       error
}

func (f *fakeRunner) Run(_ context.Context, in *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	f.lastInput = in
	return f.result, f.err
}

// TestAdapter_NameReturnsStock verifies the canonical identifier.
func TestAdapter_NameReturnsStock(t *testing.T) {
	a := stock.NewAdapter(nil) // nil runner tolerated by Name/Capabilities (no methods invoked)
	if got := a.Name(); got != "stock" {
		t.Fatalf("Name() = %q, want stock", got)
	}
}

// TestAdapter_CapabilitiesAdvertisesFetchOnly verifies that Stock
// declares CapabilityFetch and CapabilityVideo but NOT CapabilitySearch
// (Stock is fetch-only by design; one SearchProvider negative assertion
// keeps the segregation intact under future refactors).
func TestAdapter_CapabilitiesAdvertisesFetchOnly(t *testing.T) {
	a := stock.NewAdapter(nil)
	caps := a.Capabilities()
	if !hasCap(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() missing CapabilityFetch: %v", caps)
	}
	if !hasCap(caps, providers.CapabilityVideo) {
		t.Errorf("Capabilities() missing CapabilityVideo: %v", caps)
	}
	if hasCap(caps, providers.CapabilitySearch) {
		t.Errorf("Capabilities() must NOT declare CapabilitySearch (Stock is fetch-only): %v", caps)
	}
}

// TestAdapter_DoesNotImplementSearchProvider is a structural guard
// mirroring artlist/youtube — keeps the segregation intact under
// future refactors. Type assertion on `any` checks the underlying
// type, not the runtime value (so a typed-nil pointer is sufficient
// to validate).
func TestAdapter_DoesNotImplementSearchProvider(t *testing.T) {
	a := stock.NewAdapter(nil)
	if _, ok := any(a).(providers.SearchProvider); ok {
		t.Fatal("stock Adapter must NOT satisfy SearchProvider")
	}
}

// TestFetch_NilRunnerReturnsErrSourceNotWired protects against
// production-wired nil pointers. Same contract as artlist/youtube.
func TestFetch_NilRunnerReturnsErrSourceNotWired(t *testing.T) {
	a := stock.NewAdapter(nil)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if !errors.Is(err, stock.ErrSourceNotWired) {
		t.Fatalf("Fetch(nil runner) err = %v, want ErrSourceNotWired", err)
	}
}

// TestFetch_EmptySourceRef_ReturnsError protects against bad input —
// empty URL is a programmer error, not a transient one. Uses errors.Is
// to allow wrapper-style error messages.
func TestFetch_EmptySourceRef_ReturnsError(t *testing.T) {
	fr := &fakeRunner{}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{SourceRef: ""})
	if err == nil {
		t.Fatal("Fetch(empty SourceRef) err = nil, want non-nil")
	}
	// lastInput must NOT have been populated; the empty-URL guard
	// short-circuits before invoking the runner.
	if fr.lastInput != nil {
		t.Errorf("runner.Run called despite empty SourceRef; lastInput = %v", fr.lastInput)
	}
}

// TestFetch_DispatchesViaDirectURLsOnly verifies the adapter routes
// ONLY via the DirectURLs path (Stock is fetch-only, NEVER search).
// The captured lastInput must have SearchQueries empty and exactly
// one entry in DirectURLs matching the request.
func TestFetch_DispatchesViaDirectURLsOnly(t *testing.T) {
	staged := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(staged, []byte("payload"), 0644); err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	fr := &fakeRunner{
		result: &stockpipeline.PipelineResult{
			Chunks: []stockpipeline.ChunkResult{
				{LocalPath: staged},
			},
		},
	}
	a := stock.NewAdapter(fr)
	got, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err != nil {
		t.Fatalf("Fetch(...) err = %v", err)
	}
	if got == nil {
		t.Fatal("Fetch(...) returned nil FetchedAsset without error")
	}
	if got.LocalPath != staged {
		t.Errorf("FetchedAsset.LocalPath = %q, want %q", got.LocalPath, staged)
	}
	if got.Bytes != int64(len("payload")) {
		t.Errorf("FetchedAsset.Bytes = %d, want %d", got.Bytes, len("payload"))
	}
	if got.FetchedAt.IsZero() {
		t.Error("FetchedAsset.FetchedAt is zero; expected now()")
	}
	if fr.lastInput == nil {
		t.Fatal("runner.lastInput not captured — Fetch did not invoke runner.Run")
	}
	if len(fr.lastInput.DirectURLs) != 1 || fr.lastInput.DirectURLs[0] != "https://example.com/a.mp4" {
		t.Errorf("RunInput.DirectURLs = %v, want [https://example.com/a.mp4]", fr.lastInput.DirectURLs)
	}
	if len(fr.lastInput.SearchQueries) != 0 {
		t.Errorf("RunInput.SearchQueries must be empty for Fetch, got %v", fr.lastInput.SearchQueries)
	}
}

// TestFetch_NoChunks_ReturnsError protects against silent-empty
// success when the pipeline emitted zero chunks. Operator should
// see an explicit error rather than a zero-value FetchedAsset.
func TestFetch_NoChunks_ReturnsError(t *testing.T) {
	fr := &fakeRunner{result: &stockpipeline.PipelineResult{Chunks: nil}}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(no chunks) err = nil, want non-nil")
	}
}

// TestFetch_PipelineRunError_Wrapped protects against silent error
// loss. The adapter wraps runner.Run errors so callers see the
// underlying cause via errors.Is / errors.As unwrap.
func TestFetch_PipelineRunError_Wrapped(t *testing.T) {
	sentinel := errors.New("pipeline unreachable")
	fr := &fakeRunner{err: sentinel}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(pipeline err) err = nil, want non-nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Fetch error does not wrap sentinel: %v", err)
	}
}

// TestFetch_EmptyLocalPath_ReturnsError protects against successful-
// lookup / zero-path edge cases where the pipeline reported a chunk
// but its LocalPath is empty (corrupted result state).
func TestFetch_EmptyLocalPath_ReturnsError(t *testing.T) {
	fr := &fakeRunner{
		result: &stockpipeline.PipelineResult{
			Chunks: []stockpipeline.ChunkResult{{LocalPath: ""}},
		},
	}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(empty LocalPath) err = nil, want non-nil")
	}
}

// hasCap is a small predicate for slice membership. Avoid pulling
// in slices.Contains just for a one-off check inside this file.
func hasCap(caps []providers.Capability, c providers.Capability) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

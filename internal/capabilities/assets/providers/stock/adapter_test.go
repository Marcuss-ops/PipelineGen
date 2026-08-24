package assets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	stock "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

// Compile-time interface guards: catches interface drift at build time.
// The Adapter satisfies both SearchProvider and FetchProvider.
var (
	_ providers.SearchProvider = (*stock.Adapter)(nil)
	_ providers.FetchProvider  = (*stock.Adapter)(nil)
)

// fakeRunner is a minimal stub of stockpipeline.stockRunner. It captures the
// most-recent RunInput / stagedURL / searchQuery and returns canned outputs so
// unit tests can verify dispatch + happy-path mapping without standing up a
// real *stockpipeline.Service (which carries a heavy Drive+Jobs+AssetIndex
// dependency chain).
type fakeRunner struct {
	lastInput   *stockpipeline.RunInput
	result      *stockpipeline.PipelineResult
	err         error
	stagedURL   string
	staged      *stockpipeline.StagedSource
	stageErr    error
	searchQuery string
	searchLimit int
	sources     []stockpipeline.VideoSource
	searchErr   error
}

func (f *fakeRunner) Run(_ context.Context, in *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	f.lastInput = in
	return f.result, f.err
}

func (f *fakeRunner) StageSource(_ context.Context, url string) (*stockpipeline.StagedSource, error) {
	f.stagedURL = url
	return f.staged, f.stageErr
}

func (f *fakeRunner) Search(_ context.Context, query string, limit int) ([]stockpipeline.VideoSource, error) {
	f.searchQuery = query
	f.searchLimit = limit
	return f.sources, f.searchErr
}

// TestAdapter_NameReturnsStock verifies the canonical identifier.
func TestAdapter_NameReturnsStock(t *testing.T) {
	a := stock.NewAdapter(nil) // nil runner tolerated by Name/Capabilities (no methods invoked)
	if got := a.Name(); got != "stock" {
		t.Fatalf("Name() = %q, want stock", got)
	}
}

// TestAdapter_CapabilitiesAdvertisesSearchFetchAndVideo verifies that
// Stock declares CapabilitySearch, CapabilityFetch and CapabilityVideo.
func TestAdapter_CapabilitiesAdvertisesSearchFetchAndVideo(t *testing.T) {
	a := stock.NewAdapter(nil)
	caps := a.Capabilities()
	if !hasCap(caps, providers.CapabilitySearch) {
		t.Errorf("Capabilities() missing CapabilitySearch: %v", caps)
	}
	if !hasCap(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() missing CapabilityFetch: %v", caps)
	}
	if !hasCap(caps, providers.CapabilityVideo) {
		t.Errorf("Capabilities() missing CapabilityVideo: %v", caps)
	}
}

// TestAdapter_ImplementsSearchProvider is a structural guard mirroring
// artlist/youtube. Type assertion on `any` checks the underlying type,
// not the runtime value (so a typed-nil pointer is sufficient to
// validate).
func TestAdapter_ImplementsSearchProvider(t *testing.T) {
	a := stock.NewAdapter(nil)
	if _, ok := any(a).(providers.SearchProvider); !ok {
		t.Fatal("stock Adapter must satisfy SearchProvider")
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

// TestFetch_DispatchesViaStageSource verifies the adapter routes
// through StageSource (NOT Run — Blocco 2a). The captured stagedURL
// must match the request SourceRef.
func TestFetch_DispatchesViaStageSource(t *testing.T) {
	staged := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(staged, []byte("payload"), 0644); err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	fr := &fakeRunner{
		staged: &stockpipeline.StagedSource{
			LocalPath: staged,
			Bytes:     int64(len("payload")),
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
	if fr.stagedURL != "https://example.com/a.mp4" {
		t.Errorf("StageSource url = %q, want https://example.com/a.mp4", fr.stagedURL)
	}
	if fr.lastInput != nil {
		t.Errorf("runner.Run must NOT be called from Fetch after Blocco 2a; lastInput = %v", fr.lastInput)
	}
}

// TestFetch_NilStaged_ReturnsError protects against nil StagedSource
// returned by StageSource. Blocco 2a: StageSource must never return
// nil without an error.
func TestFetch_NilStaged_ReturnsError(t *testing.T) {
	fr := &fakeRunner{staged: nil}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(nil staged) err = nil, want non-nil")
	}
}

// TestFetch_StageSourceError_Wrapped protects against silent error
// loss. The adapter wraps StageSource errors so callers see the
// underlying cause via errors.Is / errors.As unwrap. Blocco 2a.
func TestFetch_StageSourceError_Wrapped(t *testing.T) {
	sentinel := errors.New("stage source unreachable")
	fr := &fakeRunner{stageErr: sentinel}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(stage err) err = nil, want non-nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Fetch error does not wrap sentinel: %v", err)
	}
}

// TestFetch_EmptyLocalPath_ReturnsError protects against successful
// staging with empty LocalPath (corrupted StagedSource state).
func TestFetch_EmptyLocalPath_ReturnsError(t *testing.T) {
	fr := &fakeRunner{
		staged: &stockpipeline.StagedSource{LocalPath: ""},
	}
	a := stock.NewAdapter(fr)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://example.com/a.mp4",
	})
	if err == nil {
		t.Fatal("Fetch(empty LocalPath) err = nil, want non-nil")
	}
}

// TestSearch_NilRunnerReturnsErrSourceNotWired protects against
// production-wired nil pointers, mirroring Fetch.
func TestSearch_NilRunnerReturnsErrSourceNotWired(t *testing.T) {
	a := stock.NewAdapter(nil)
	_, err := a.Search(context.Background(), providers.SearchRequest{
		Query: "boxing",
		Limit: 5,
	})
	if !errors.Is(err, stock.ErrSourceNotWired) {
		t.Fatalf("Search(nil runner) err = %v, want ErrSourceNotWired", err)
	}
}

// TestSearch_EmptyQuery_ReturnsError protects against bad input —
// empty query is a programmer error, not a transient one.
func TestSearch_EmptyQuery_ReturnsError(t *testing.T) {
	fr := &fakeRunner{}
	a := stock.NewAdapter(fr)
	_, err := a.Search(context.Background(), providers.SearchRequest{Query: ""})
	if err == nil {
		t.Fatal("Search(empty query) err = nil, want non-nil")
	}
	if fr.searchQuery != "" {
		t.Errorf("runner.Search called despite empty query; searchQuery = %q", fr.searchQuery)
	}
}

// TestSearch_MapsVideoSourceToCandidate verifies the happy path maps
// VideoSource → providers.Candidate with the canonical YouTube URL as
// SourceRef/ExternalID and the video media type.
func TestSearch_MapsVideoSourceToCandidate(t *testing.T) {
	fr := &fakeRunner{
		sources: []stockpipeline.VideoSource{
			{URL: "https://www.youtube.com/watch?v=abc123", Title: "Mayweather highlights", DurationSec: 62.5},
			{URL: "https://www.youtube.com/watch?v=def456", Title: "", DurationSec: 0},
		},
	}
	a := stock.NewAdapter(fr)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "Floyd Mayweather", Limit: 2})
	if err != nil {
		t.Fatalf("Search(...) err = %v", err)
	}
	if fr.searchQuery != "Floyd Mayweather" {
		t.Errorf("searchQuery = %q, want Floyd Mayweather", fr.searchQuery)
	}
	if fr.searchLimit != 2 {
		t.Errorf("searchLimit = %d, want 2", fr.searchLimit)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(res.Candidates))
	}
	c0 := res.Candidates[0]
	if c0.SourceName != "stock" {
		t.Errorf("c0.SourceName = %q, want stock", c0.SourceName)
	}
	if c0.SourceRef != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("c0.SourceRef = %q, want youtube URL", c0.SourceRef)
	}
	if c0.ExternalID != "https://www.youtube.com/watch?v=abc123" {
		t.Errorf("c0.ExternalID = %q, want youtube URL", c0.ExternalID)
	}
	if c0.Title != "Mayweather highlights" {
		t.Errorf("c0.Title = %q, want Mayweather highlights", c0.Title)
	}
	if c0.MediaType != "video" {
		t.Errorf("c0.MediaType = %q, want video", c0.MediaType)
	}
	if c0.DurationMs != int64(62.5*1000) {
		t.Errorf("c0.DurationMs = %d, want %d", c0.DurationMs, int64(62.5*1000))
	}
}

// TestSearch_RunnerError_Wrapped protects against silent error loss.
func TestSearch_RunnerError_Wrapped(t *testing.T) {
	sentinel := errors.New("channel lister down")
	fr := &fakeRunner{searchErr: sentinel}
	a := stock.NewAdapter(fr)
	_, err := a.Search(context.Background(), providers.SearchRequest{Query: "boxing", Limit: 1})
	if err == nil {
		t.Fatal("Search(search err) err = nil, want non-nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Search error does not wrap sentinel: %v", err)
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

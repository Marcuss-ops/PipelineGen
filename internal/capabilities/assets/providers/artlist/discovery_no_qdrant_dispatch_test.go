// Chip 2 (June 2026, fix-FASE9 followups plan) regression test:
// discovery-time SearchLiveAndSave MUST NOT emit an
// asset.index.requested outbox event. Before chip 2 the inline call
// to dispatcher.EnqueueAndIndex put a half-built asset (no real hash,
// no Drive file id, no upload completed) into Qdrant for some
// seconds between discovery and upload-commit. The fix routes
// discovery through SaveDiscoveredAsset (LifecycleState=STAGING +
// IndexState=DISCOVERED, no outbox event); the canonical
// asset.index.requested envelope is emitted later, by the artlist.run
// processing post-processing finalizer, when the clip is fully
// populated.
//
// The test exercises the discovery path via a stub Searcher that
// returns a single canned candidate, and uses a recordingDispatcher
// to count EnqueueAndIndex + SaveDiscoveredAsset invocations.
package assets

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// recordingDispatcher is the test-only Dispatcher implementation that
// counts every method invocation. It satisfies both the local artlist
// Dispatcher port (5 methods) AND the upstream mutations
// AssetMutationDispatcher — only the first 4 are exercised by
// SearchLiveAndSave.
type recordingDispatcher struct {
	mu                   sync.Mutex
	enqueueAndIndexCalls int
	saveDiscoveredCalls  int
	lastSaveLife         asset.LifecycleState
	lastSaveIdx          asset.IndexState
}

func (r *recordingDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enqueueAndIndexCalls++
	return nil
}

func (r *recordingDispatcher) SaveDiscoveredAsset(_ context.Context, _ *asset.Asset, l asset.LifecycleState, idx asset.IndexState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveDiscoveredCalls++
	r.lastSaveLife = l
	r.lastSaveIdx = idx
	return nil
}

// EnqueueAndRestore / EnqueueAndDelete are present so the recorder also
// satisfies mutations.AssetMutationDispatcher (rule used by composition
// adapters asserting `var _ mutations.AssetMutationDispatcher = ...`).
// SearchLiveAndSave does not exercise them — the no-op keeps
// recordingDispatcher drop-in to any future test that does.
func (r *recordingDispatcher) EnqueueAndRestore(_ context.Context, _ string) error { return nil }
func (r *recordingDispatcher) EnqueueAndDelete(_ context.Context, _ string) error  { return nil }

// staticSearcher is the test-only Searcher implementation that returns
// a fixed candidate list per Search call. Wired into Service.scraperSearcher
// so buildSearcherChain (the level-2 append in SearchService.searchLiveWithFallbacks)
// picks it up via the CachedSearcher wrapper.
type staticSearcher struct {
	cands []Candidate
}

func (s *staticSearcher) Search(_ context.Context, _ SearchRequest) ([]Candidate, error) {
	return s.cands, nil
}

// TestSearchLiveAndSave_NoQdrantDispatchOnDiscoveryOK pins the chip-2
// discovery-time invariant on the success path:
//
//   - dispatcher.EnqueueAndIndex is NEVER called (the previous inline
//     call produced premature Qdrant indexing of incomplete assets).
//   - dispatcher.SaveDiscoveredAsset is called exactly once per candidate,
//     stamping LifecycleState=STAGING + IndexState=DISCOVERED (canonical).
//
// Failure path: SearchLiveAndSave returns "no search providers
// configured" when Service has no Searchers wired (recordingDispatcher
// counts both EnqueueAndIndex and SaveDiscoveredAsset at 0 then);
// success path runs the per-clip loop end-to-end through the stub
// Searcher + recordingDispatcher pair.
func TestSearchLiveAndSave_NoQdrantDispatchOnDiscoveryOK(t *testing.T) {
	rec := &recordingDispatcher{}

	svc := &Service{
		log:        zap.NewNop(),
		assetStore: nil, // assetStore nil → SearchLiveAndSave's merge block is no-op (defensive guard)
		// Wire a Searcher so buildSearcherChain returns a non-nil chain.
		// The chain wraps the static Searcher in CachedSearcher (which
		// delegates through to it on cache miss). liveCache=nil is
		// tolerated by CachedSearcher's no-op-on-miss path.
		scraperSearcher: &staticSearcher{cands: []Candidate{{
			ID:         "chip2-discovery-001",
			Title:      "Forest in misty dawn",
			SourceRef:  "https://artlist.io/hls/chip2-discovery-001.m3u8",
			PageURL:    "https://artlist.io/clip/chip2-discovery-001",
			SourceName: "artlist",
		}}},
	}
	ss, err := NewSearchService(svc, rec)
	if err != nil {
		t.Fatalf("NewSearchService(rec): %v", err)
	}

	ctx := context.Background()
	resp, err := ss.SearchLiveAndSave(ctx, "forest", 1)
	if err != nil {
		t.Fatalf("SearchLiveAndSave: %v", err)
	}
	if resp == nil {
		t.Fatalf("response is nil on success path")
	}
	if got, want := len(resp.Clips), 1; got != want {
		t.Fatalf("resp.Clips len = %d; want %d (one per discovery candidate)", got, want)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if rec.enqueueAndIndexCalls != 0 {
		t.Errorf("chip 2 invariant violated: dispatcher.EnqueueAndIndex called %d times on discovery OK; want 0 (discovery must NOT emit outbox index events — premature Qdrant indexing of incomplete assets)", rec.enqueueAndIndexCalls)
	}
	if rec.saveDiscoveredCalls != 1 {
		t.Errorf("dispatcher.SaveDiscoveredAsset called %d times; want 1 (one per discovery candidate)", rec.saveDiscoveredCalls)
	}
	if rec.lastSaveLife != asset.StateStaging {
		t.Errorf("last SaveDiscoveredAsset lifecycle_state = %q; want STAGING", rec.lastSaveLife)
	}
	if rec.lastSaveIdx != asset.StateDiscovered {
		t.Errorf("last SaveDiscoveredAsset index_state = %q; want DISCOVERED", rec.lastSaveIdx)
	}
}

// TestSearchLiveAndSave_NoSearchersNoMutationLockAndNoPanic locks the
// negative path: when no Searcher is wired, the discovery chain fails
// closed at SearchLive ("no search providers configured"), the per-clip
// loop never executes, and recordingDispatcher observes zero
// dispatches. This guards against a regression where a future refactor
// moves the dispatcher call OUTSIDE the per-clip loop and accidentally
// dispatches even on the failure path.
func TestSearchLiveAndSave_NoSearchersNoMutationLockAndNoPanic(t *testing.T) {
	rec := &recordingDispatcher{}
	svc := &Service{log: zap.NewNop()}
	ss, err := NewSearchService(svc, rec)
	if err != nil {
		t.Fatalf("NewSearchService(rec): %v", err)
	}
	resp, err := ss.SearchLiveAndSave(context.Background(), "anyterm", 1)
	if err == nil {
		t.Fatalf("want error from no-Searchers path; got nil (resp=%v)", resp)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.enqueueAndIndexCalls != 0 {
		t.Errorf("EnqueueAndIndex called on failure path: %d (chip 2 invariant: dispatches forbidden on every path)", rec.enqueueAndIndexCalls)
	}
}

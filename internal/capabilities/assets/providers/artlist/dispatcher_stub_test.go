// Package artlist — test-only stubs for the Dispatcher port.
//
// PR1 (User directive, June 2026): artlist.NewSearchService is fail-closed
// (returns ErrAssetMutationDispatcherUnavailable when dispatcher is nil).
// artlist.NewService propagates the constructor error. Tests that
// pre-date QDRANT-002 PR7 used to construct the service with `Dispatcher`
// field left zero (nil) and relied on the legacy
// `if dispatcher != nil { EnqueueAndIndex } else { assetStore.Upsert }`
// fallback inside SearchLiveAndSave. That path is REMOVED. To keep
// those tests runnable without spinning up the canonical outbox pool
// (which would force every test to wire outbox_events tables + Pool +
// DB), we provide a test stub that satisfies the Dispatcher port and
// delegates to the existing *assets.ClipsRepository.UpsertClip path.
//
// PRODUCTION CODE MUST NOT USE THIS TYPE. The stub deliberately bypasses
// the canonical outbox flow that production requires (atomic media_assets
// upsert + outbox enqueue + IndexClip worker). Its sole purpose is to
// keep artlist-package unit tests isolated.
//
// TODO(QDRANT-004 PR-A — User task #2 follow-up): when the canonical
// AssetMutationDispatcher interface lands, retire this stub in favour of
// a typed Dispatcher port test double that lives in
// internal/application/assets/mutations/testdouble.
package artlist

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrQdrantUnavailable is the canonical typed sentinel for Gate 10
// Qdrant-failure tests. Matches the real-world error a caller would
// receive when the Qdrant cluster is down, unreachable, or returns
// a non-2xx response on the upsert endpoint. The artlist pipeline
// must treat this as a NON-FATAL side-effect (the clip is still
// processed; the run still reports OK; the index_state simply stays
// in DISCOVERED/INDEXING/INDEXING_FAILED rather than transitioning
// to INDEXED).
var ErrQdrantUnavailable = errors.New("qdrant unavailable: simulated indexing failure (network or 5xx)")

// failingDispatcherForArtlist is a Gate 10 test double that wraps
// stubDispatcherForArtlist (which does the canonical media_assets
// upsert) and returns ErrQdrantUnavailable from EnqueueAndIndex to
// simulate a Qdrant indexing failure during the dispatch step.
//
// All other Dispatcher methods (SaveDiscoveredAsset, EnqueueAndRestore,
// EnqueueAndDelete) are inherited unchanged from the embedded stub
// via Go struct embedding (anonymous field promotes all methods).
//
// PRODUCTION CODE MUST NOT USE THIS TYPE. Its sole purpose is to
// verify the fail-soft contract documented in Gate 10:
//
//   - index_state must NOT transition to INDEXED when Qdrant fails
//   - Processed count must be unaffected by the Qdrant failure
//   - The Artlist run overall must NOT fail closed
type failingDispatcherForArtlist struct {
	stubDispatcherForArtlist
}

// Compile-time: failingDispatcherForArtlist satisfies the Dispatcher
// port (via embedded stubDispatcherForArtlist). Drift at the port
// signature surfaces as a build failure.
var _ Dispatcher = (*failingDispatcherForArtlist)(nil)

// EnqueueAndIndex simulates a Qdrant indexing failure: the canonical
// media_assets upsert that the embedded stub would do is SKIPPED
// (because the production code's contract is that the upsert+outbox
// pair happens in a single atomic transaction, and the test exercises
// the failure path at the Qdrant-upsert boundary, AFTER the atomic
// pair succeeds). Returns ErrQdrantUnavailable to let the caller
// observe the failure without crashing.
//
// godlike/07 no-fake-availability: returning a typed error that
// matches what a real Qdrant failure would produce (connection
// refused, 503, timeout) — NOT a generic "fail" string.
func (f *failingDispatcherForArtlist) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	return ErrQdrantUnavailable
}

// stubDispatcherForArtlist is the test-only Dispatcher adapter used by
// artlist-package tests. Production code MUST NOT construct this type.
type stubDispatcherForArtlist struct {
	// repo is the canonical SQLite-backed clips repository the stub writes
	// to. nil repo = no-op stub (EnqueueAndIndex returns nil immediately).
	repo *assets.ClipsRepository
}

// Compile-time assertion: stubDispatcherForArtlist satisfies the
// production-side Dispatcher port. Drift at the port signature will be
// caught at `go build` time, not at test runtime.
var _ Dispatcher = (*stubDispatcherForArtlist)(nil)

// EnqueueAndIndex implements the Dispatcher port by delegating to the
// legacy repo.UpsertClip path. The matching production behaviour is
// "atomic upsert + outbox enqueue"; the stub deliberately trades the
// outbox half for test isolation. Callers MUST NOT depend on the stub
// to model production semantics.
func (s *stubDispatcherForArtlist) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if s == nil || s.repo == nil || clip == nil {
		return nil
	}
	return s.repo.UpsertClip(ctx, clip)
}

// SaveDiscoveredAsset implements the dispatcher port's discovery-only
// upsert path (chip 2, June 2026). Mirrors EnqueueAndIndex's stub
// behaviour: persists the row via repo.UpsertClip with no outbox event
// emission (test isolation — production behaviour is also "no outbox
// event" so the stub matches semantics, not just the call surface).
func (s *stubDispatcherForArtlist) SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if s == nil || s.repo == nil || clip == nil {
		return nil
	}
	clip.LifecycleState = lifecycle
	clip.SetMetadataString("index_state", string(idx))
	return s.repo.UpsertClip(ctx, clip)
}

// EnqueueAndRestore is the test-stub counterpart for the
// mutations.AssetMutationDispatcher.EnqueueAndRestore call site.
// The stub is a no-op so tests that don't exercise the restore path
// don't have to spin up a handler; tests that DO exercise restore
// should inject a more specific double (a typed testdouble in
// internal/application/assets/mutations/testdouble ships with
// task 3 of 5).
func (s *stubDispatcherForArtlist) EnqueueAndRestore(_ context.Context, _ string) error {
	return nil
}

// EnqueueAndDelete is the test-stub counterpart for the
// mutations.AssetMutationDispatcher.EnqueueAndDelete call site.
// No-op for tests that don't exercise the delete half; the test
// stub deliberately preserves the backward-compatible shape so the
// existing pre-PR failure-closed tests (failclosed_test.go) keep
// compiling after the AssetMutationDispatcher interface was
// declared.
func (s *stubDispatcherForArtlist) EnqueueAndDelete(_ context.Context, _ string) error {
	return nil
}

// stubRunRepoForArtlist is the test-only RunRepository adapter used by
// artlist-package tests. PRODUCTION CODE MUST NOT USE THIS TYPE.
//
// PR-ARTLIST-PERSIST-FIX (2026-07-04): the godlike/07 fail-closed
// gate in NewService rejects nil RunRepository at composition;
// tests that don't exercise the artlist_runs aggregate write path
// (most service-layer unit tests) need a no-op implementation to
// satisfy NewService. The stub collapses every Record call to nil
// — it does NOT verify the row was written; tests that exercise
// the aggregate path should inject a more specific double (e.g.
// an in-memory map-backed struct).
type stubRunRepoForArtlist struct{}

// Compile-time assertion: stubRunRepoForArtlist satisfies the
// production-side RunRepository port. Drift in the port signature
// surfaces as a build failure rather than a runtime panic.
var _ RunRepository = (*stubRunRepoForArtlist)(nil)

// Record is a no-op for tests that don't exercise the artlist_runs
// aggregate write path. Production code goes through the SQLite-
// backed ArtlistRunsRepository in
// internal/platform/sqlite/assets/artlist_runs_repository.go.
func (s *stubRunRepoForArtlist) Record(_ context.Context, _ RunRecord) error {
	return nil
}

// LatestRun is a no-op for tests that don't exercise the
// diagnostics-endpoint read path. Returns (nil, nil) — equivalent
// to "fresh install" semantics so tests asserting `LatestRun == nil`
// see the same shape the production empty-table path produces.
func (s *stubRunRepoForArtlist) LatestRun(_ context.Context) (*LatestRunSummary, error) {
	return nil, nil
}

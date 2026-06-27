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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

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

// Package ingest — PR 7 (June 2026, codex/qdrant-app-writers-fail-closed)
// dispatcher fail-closed contract test for clipStoreAdapter.Upsert
// (the lifecycle-targeted writer migrated from raw repo.Upsert to
// dispatcher.EnqueueAndIndex in PR 7).
//
// Pattern mirrors internal/api/assets/clips/dispatcher_fail_closed_test.go
// (PR 6 precedent): nil dispatcher returns wrapped
// mutations.ErrDispatcherUnavailable so the composition regression is
// operator-visible. The locations.Upsert narrow-port writes below the
// dispatcher check remain unchanged (MIXED — see Phase A commit for the
// rationale; their migration lands in a future wave, not PR 7).

package ingest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// pr7StubDispatcher satisfies the production
// mutations.AssetMutationDispatcher interface (EnqueueAndIndex +
// EnqueueAndRestore + EnqueueAndDelete) for the PR 7 adapter test
// surface. PRODUCTION CODE MUST NOT USE THIS TYPE.
//
// All three methods match the production signature exactly.
type pr7StubDispatcher struct {
	calls atomic.Int32
}

func (r *pr7StubDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	r.calls.Add(1)
	return nil
}

// EnqueueAndRestore takes a string assetID. PR 7 sites don't invoke
// restore; stub returns nil.
func (r *pr7StubDispatcher) EnqueueAndRestore(_ context.Context, _ string) error {
	return nil
}

// EnqueueAndDelete takes a string assetID. PR 7 sites don't invoke
// delete; stub returns nil.
func (r *pr7StubDispatcher) EnqueueAndDelete(_ context.Context, _ string) error {
	return nil
}

var _ mutations.AssetMutationDispatcher = (*pr7StubDispatcher)(nil)

// TestPR7_ClipStoreAdapter_Upsert_NilDispatcher_FailClosed pins the
// strict fail-closed behaviour of clipStoreAdapter.Upsert when
// constructed with dispatcher=nil.
//
// Implementation note: clipStoreAdapter.Upsert reads `rec.ID, rec.Source,
// rec.Name, ...` BEFORE the dispatcher check (to build the *asset.Asset
// argument). A nil rec would panic at that field-derivation step before
// the dispatcher nil-check fires. The test supplies a minimal non-nil
// artifacts.MediaRecord so the dispatcher nil-check is the surface that
// actually rejects the call.
func TestPR7_ClipStoreAdapter_Upsert_NilDispatcher_FailClosed(t *testing.T) {
	a := NewClipStoreAdapter(
		nil, // db
		nil, // repo
		nil, // querySvc
		nil, // locations
		nil, // processing
		nil, // dispatcher nil → strict fail-closed
	)
	require.NotNil(t, a)

	rec := &artifacts.MediaRecord{
		ID:   "pr7-clip-001",
		Name: "fixture",
	}
	err := a.Upsert(context.Background(), rec)
	require.Error(t, err, "PR 7 fail-closed: Upsert with nil dispatcher must error")
	assert.True(t, errors.Is(err, mutations.ErrDispatcherUnavailable),
		"PR 7 fail-closed: error must wrap mutations.ErrDispatcherUnavailable; got: %v", err)
}

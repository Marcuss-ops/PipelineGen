// Package artifacts — PR 7 (June 2026, codex/qdrant-app-writers-fail-closed)
// dispatcher fail-closed contract test for ClipsRegistry.UpsertMedia
// (the artifacts-targeted writer migrated from raw assetRepo.Upsert
// to dispatcher.EnqueueAndIndex in PR 7).
//
// Pattern mirrors internal/api/assets/clips/dispatcher_fail_closed_test.go
// (PR 6 precedent): nil dispatcher returns wrapped
// mutations.ErrDispatcherUnavailable so the composition regression is
// operator-visible. The narrow-port locations.Upsert writes below the
// dispatcher check remain unchanged (MIXED — see Phase A commit).

package artifacts

import (
	"context"
	"errors"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
)

// pr7StubDispatcher satisfies the production
// mutations.AssetMutationDispatcher interface (EnqueueAndIndex +
// EnqueueAndRestore + EnqueueAndDelete) for the PR 7 ClipsRegistry
// test surface. PRODUCTION CODE MUST NOT USE THIS TYPE.
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
// delete; soft-delete lands on the detail.Repository narrow port.
func (r *pr7StubDispatcher) EnqueueAndDelete(_ context.Context, _ string) error {
	return nil
}

var _ mutations.AssetMutationDispatcher = (*pr7StubDispatcher)(nil)

// TestPR7_ClipsRegistry_UpsertMedia_NilDispatcher_FailClosed pins the
// strict fail-closed behaviour of ClipsRegistry.UpsertMedia when
// constructed with dispatcher=nil.
//
// Implementation note: ClipsRegistry.UpsertMedia derives the *asset.Asset
// from `rec.ID, rec.Source, rec.Name, ...` field reads BEFORE the
// dispatcher check fires (to populate the dispatcher's first arg). A
// nil rec would panic at that field-derivation step. The test supplies
// a minimal non-nil MediaRecord so the dispatcher nil-check is the
// surface that actually rejects the call.
func TestPR7_ClipsRegistry_UpsertMedia_NilDispatcher_FailClosed(t *testing.T) {
	r := NewClipsRegistry(
		nil, // db
		nil, // assets.Repository
		nil, // querySvc
		nil, // locations
		nil, // processing
		nil, // dispatcher nil → strict fail-closed
	)
	require.NotNil(t, r)

	rec := &MediaRecord{ID: "pr7-reg-001"}
	err := r.UpsertMedia(context.Background(), rec)
	require.Error(t, err, "PR 7 fail-closed: UpsertMedia with nil dispatcher must error")
	assert.True(t, errors.Is(err, mutations.ErrDispatcherUnavailable),
		"PR 7 fail-closed: error must wrap mutations.ErrDispatcherUnavailable; got: %v", err)
}

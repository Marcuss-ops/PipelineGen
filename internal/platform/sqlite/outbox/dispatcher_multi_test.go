package outbox

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeClipsUpserter records every UpsertClipTx invocation in a slice.
// The optional err field lets a test inject a failure to verify the
// MultiClipsUpserter surfaces the error unchanged.
type fakeClipsUpserter struct {
	called []string
	err    error
}

func (f *fakeClipsUpserter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if f.err != nil {
		return f.err
	}
	if clip != nil {
		f.called = append(f.called, clip.ID)
	}
	return nil
}

// TestMultiClipsUpserterRoutesBySource covers the happy-path dispatch:
// each per-source clip goes to the matching repo, and an unknown source
// falls through to the default (which here is the youtube repo, mirroring
// the prior silent fallback in catalogsync).
func TestMultiClipsUpserterRoutesBySource(t *testing.T) {
	yt := &fakeClipsUpserter{}
	stock := &fakeClipsUpserter{}
	artlist := &fakeClipsUpserter{}

	m := NewMultiClipsUpserter(
		map[string]ClipsUpserter{
			"youtube": yt,
			"stock":   stock,
			"artlist": artlist,
		},
		yt, // default
		zap.NewNop(),
	)

	ctx := context.Background()
	require.NoError(t, m.UpsertClipTx(ctx, nil, &asset.Asset{ID: "yt_a", Source: "youtube"}))
	require.NoError(t, m.UpsertClipTx(ctx, nil, &asset.Asset{ID: "stock_b", Source: "stock"}))
	require.NoError(t, m.UpsertClipTx(ctx, nil, &asset.Asset{ID: "artlist_c", Source: "artlist"}))
	require.NoError(t, m.UpsertClipTx(ctx, nil, &asset.Asset{ID: "unknown_d", Source: "something_else"}))

	assert.Equal(t, []string{"yt_a", "unknown_d"}, yt.called, "youtube repo + default fallback both target yt")
	assert.Equal(t, []string{"stock_b"}, stock.called)
	assert.Equal(t, []string{"artlist_c"}, artlist.called)
}

// TestMultiClipsUpserter_NoDefault verifies that an unknown source with
// no default configured fails loudly — preferable to silently dropping a
// write or routing to the wrong DB.
func TestMultiClipsUpserter_NoDefault(t *testing.T) {
	yt := &fakeClipsUpserter{}
	m := NewMultiClipsUpserter(map[string]ClipsUpserter{"youtube": yt}, nil, zap.NewNop())

	err := m.UpsertClipTx(context.Background(), nil, &asset.Asset{ID: "x", Source: "unknown"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no repository")
	assert.Empty(t, yt.called, "no repo should be touched on a noisy-failure path")
}

// TestMultiClipsUpserter_NilClipErrors protects against a regression
// where the router would dereference a nil clip. A programming-error
// guard returns error.New, not a panic.
func TestMultiClipsUpserter_NilClipErrors(t *testing.T) {
	yt := &fakeClipsUpserter{}
	m := NewMultiClipsUpserter(map[string]ClipsUpserter{"youtube": yt}, yt, zap.NewNop())
	err := m.UpsertClipTx(context.Background(), nil, nil)
	require.Error(t, err)
}

// TestMultiClipsUpserter_NilReceiverErrors covers the case where the
// router itself is never constructed. Same philosophy as the nil clip
// guard: return error so the call site stays compilable.
func TestMultiClipsUpserter_NilReceiverErrors(t *testing.T) {
	var m *MultiClipsUpserter
	err := m.UpsertClipTx(context.Background(), nil, &asset.Asset{ID: "x", Source: "youtube"})
	require.Error(t, err)
}

// TestMultiClipsUpserter_ErrorFromInnerSurfaces confirms that an inner
// repo's error is propagated unchanged (so the dispatcher's transaction
// manager rolls back, preserving atomicity).
func TestMultiClipsUpserter_ErrorFromInnerSurfaces(t *testing.T) {
	sentinel := errors.New("inner repo failure")
	inner := &fakeClipsUpserter{err: sentinel}
	m := NewMultiClipsUpserter(map[string]ClipsUpserter{"youtube": inner}, inner, zap.NewNop())

	err := m.UpsertClipTx(context.Background(), nil, &asset.Asset{ID: "x", Source: "youtube"})
	require.ErrorIs(t, err, sentinel)
}

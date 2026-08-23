package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	// SearchRunnerPort + sentinel errors live in ports/.
	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestNewSearchRunnerAdapter_NilConfigReturnsNil verifies the constructor
// fail-closed contract: a nil config returns nil so the composition root
// can short-circuit before any service tries to use it.
func TestNewSearchRunnerAdapter_NilConfigReturnsNil(t *testing.T) {
	log := zap.NewNop()
	got := NewSearchRunnerAdapter(nil, log)
	assert.Nil(t, got, "nil cfg must produce nil adapter (PR2 fail-closed)")

	got = NewSearchRunnerAdapter(nil, nil)
	assert.Nil(t, got, "both nil cfg and nil log must produce nil adapter")
}

// TestNewSearchRunnerAdapter_HappyPath constructs a valid adapter (cfg
// pointer non-nil; log non-nil). We do not need to populate ResolvedYtdlpPath
// for the construction check itself (path check happens lazily on call).
func TestNewSearchRunnerAdapter_HappyPath(t *testing.T) {
	cfg := &ytcfg.Config{}
	got := NewSearchRunnerAdapter(cfg, zap.NewNop())
	require.NotNil(t, got, "valid cfg + log must produce non-nil adapter")
	// Compile-time guard (re-asserted): the adapter implements the app-layer
	// SearchRunnerPort. If the port signature changes, this assertion trips.
	var _ youtubedto.SearchRunnerPort = got
}

// TestSearchRunnerAdapter_SearchLive_ContextCancel verifies the ctx.Err()
// branch: a pre-canceled context surfaces its own error WITHOUT wrapping,
// so callers can detect cancellation distinctly from a fail-closed
// infrastructure error.
func TestSearchRunnerAdapter_SearchLive_ContextCancel(t *testing.T) {
	cfg := &ytcfg.Config{}
	a := NewSearchRunnerAdapter(cfg, zap.NewNop())
	require.NotNil(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := a.SearchLive(ctx, "test query", 10, "relevance")
	assert.Nil(t, results)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled,
		"ctx.Err() should be returned unwrapped so callers can branch")
	// Critically: the wrapped error MUST NOT include ErrSearchRunnerUnavailable
	// because the failure is caller-driven, not infrastructure-driven.
	assert.NotErrorIs(t, err, youtubedto.ErrSearchRunnerUnavailable,
		"caller-driven cancellation must not look like infrastructure unavailability")
}

// TestSearchRunnerAdapter_GetVideoInfo_ContextCancel mirrors the SearchLive
// cancellation test for the GetVideoInfo method.
func TestSearchRunnerAdapter_GetVideoInfo_ContextCancel(t *testing.T) {
	cfg := &ytcfg.Config{}
	a := NewSearchRunnerAdapter(cfg, zap.NewNop())
	require.NotNil(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	meta, err := a.GetVideoInfo(ctx, "https://www.youtube.com/watch?v=abc123")
	assert.Nil(t, meta)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, youtubedto.ErrSearchRunnerVideoInfoUnavailable)
}

// TestSearchRunnerAdapter_NilReceiver verifies defensive programming: even
// if a typed-nil *SearchRunnerAdapter were somehow wired in, the methods
// surface ErrSearchRunnerUnavailable rather than panicking. This is the
// last-line defense for the typed-nil port scenario documented in
// pkg/portutil/isnilport.go.
func TestSearchRunnerAdapter_NilReceiver(t *testing.T) {
	var nilAdapter *SearchRunnerAdapter // typed-nil

	// SearchLive
	results, err := nilAdapter.SearchLive(context.Background(), "test", 5, "relevance")
	assert.Nil(t, results)
	require.Error(t, err)
	assert.True(t, errors.Is(err, youtubedto.ErrSearchRunnerUnavailable),
		"typed-nil receiver must return ErrSearchRunnerUnavailable, never panic")

	// GetVideoInfo
	meta, err := nilAdapter.GetVideoInfo(context.Background(), "https://www.youtube.com/watch?v=xyz")
	assert.Nil(t, meta)
	require.Error(t, err)
	assert.True(t, errors.Is(err, youtubedto.ErrSearchRunnerVideoInfoUnavailable),
		"typed-nil receiver must return ErrSearchRunnerVideoInfoUnavailable, never panic")
}

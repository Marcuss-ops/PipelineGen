package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSearchRunnerStub_RespectsCanceledContext(t *testing.T) {
	log := zap.NewNop()
	stub := &searchRunnerStub{log: log}

	t.Run("SearchLive returns context.Canceled on pre-canceled ctx", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // immediately cancel

		results, err := stub.SearchLive(ctx, "test query", 10, "relevance")
		assert.Nil(t, results)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("GetVideoInfo returns context.Canceled on pre-canceled ctx", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		meta, err := stub.GetVideoInfo(ctx, "https://www.youtube.com/watch?v=abc123")
		assert.Nil(t, meta)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("SearchLive returns empty results on non-canceled ctx (baseline)", func(t *testing.T) {
		results, err := stub.SearchLive(context.Background(), "test", 5, "relevance")
		assert.NoError(t, err)
		assert.NotNil(t, results)
		assert.Empty(t, results)
	})

	t.Run("GetVideoInfo returns non-nil DTO on non-canceled ctx (baseline)", func(t *testing.T) {
		meta, err := stub.GetVideoInfo(context.Background(), "https://www.youtube.com/watch?v=abc123")
		assert.NoError(t, err)
		assert.NotNil(t, meta)
	})
}

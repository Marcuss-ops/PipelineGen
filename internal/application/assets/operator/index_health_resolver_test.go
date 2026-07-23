package operator

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/stretchr/testify/assert"
)

func TestResolveIndexHealth(t *testing.T) {
	cases := []struct {
		name          string
		input         IndexHealthInput
		wantCode      IndexHealthCode
		wantSeverity  string
		wantTerminal  bool
		wantRetryable bool
	}{
		{
			name:         "NOT_INDEXABLE maps to NOT_INDEXABLE",
			input:        IndexHealthInput{IndexState: asset.StateNotIndexable},
			wantCode:     IndexHealthNotIndexable,
			wantSeverity: "neutral",
			wantTerminal: true,
		},
		{
			name:         "DISCOVERED maps to PENDING",
			input:        IndexHealthInput{IndexState: asset.StateDiscovered},
			wantCode:     IndexHealthPending,
			wantSeverity: "info",
			wantTerminal: false,
		},
		{
			name:         "EMBEDDED without pending outbox maps to INDEXING",
			input:        IndexHealthInput{IndexState: asset.StateEmbedded},
			wantCode:     IndexHealthIndexing,
			wantSeverity: "info",
			wantTerminal: false,
		},
		{
			name:         "EMBEDDED with pending outbox maps to PENDING",
			input:        IndexHealthInput{IndexState: asset.StateEmbedded, PendingOutboxEvents: 2},
			wantCode:     IndexHealthPending,
			wantSeverity: "info",
			wantTerminal: false,
		},
		{
			name:         "INDEXING maps to INDEXING",
			input:        IndexHealthInput{IndexState: asset.StateIndexing},
			wantCode:     IndexHealthIndexing,
			wantSeverity: "info",
			wantTerminal: false,
		},
		{
			name:         "EMBEDDING maps to EMBEDDING",
			input:        IndexHealthInput{IndexState: asset.StateEmbedding},
			wantCode:     IndexHealthEmbedding,
			wantSeverity: "info",
			wantTerminal: false,
		},
		{
			name:         "INDEXED with matching hashes maps to INDEXED",
			input:        IndexHealthInput{IndexState: asset.StateIndexed, ContentHash: "abc", IndexedContentHash: "abc"},
			wantCode:     IndexHealthIndexed,
			wantSeverity: "success",
			wantTerminal: true,
		},
		{
			name:          "INDEXED with mismatched hashes maps to STALE",
			input:         IndexHealthInput{IndexState: asset.StateIndexed, ContentHash: "abc", IndexedContentHash: "def"},
			wantCode:      IndexHealthStale,
			wantSeverity:  "warning",
			wantTerminal:  false,
			wantRetryable: true,
		},
		{
			name:          "EMBEDDING_FAILED maps to FAILED",
			input:         IndexHealthInput{IndexState: asset.StateEmbeddingFailed},
			wantCode:      IndexHealthFailed,
			wantSeverity:  "error",
			wantTerminal:  true,
			wantRetryable: true,
		},
		{
			name:          "INDEXING_FAILED maps to FAILED",
			input:         IndexHealthInput{IndexState: asset.StateIndexingFailed},
			wantCode:      IndexHealthFailed,
			wantSeverity:  "error",
			wantTerminal:  true,
			wantRetryable: true,
		},
		{
			name:          "INDEXING_SKIPPED_NO_INDEXER maps to RETRY_WAIT",
			input:         IndexHealthInput{IndexState: asset.StateIndexingSkippedNoIndexer},
			wantCode:      IndexHealthRetryWait,
			wantSeverity:  "warning",
			wantTerminal:  false,
			wantRetryable: true,
		},
		{
			name:         "DELETED maps to DELETED",
			input:        IndexHealthInput{IndexState: asset.StateDELETED},
			wantCode:     IndexHealthDeleted,
			wantSeverity: "neutral",
			wantTerminal: true,
		},
		{
			name:         "unknown index state maps to UNKNOWN",
			input:        IndexHealthInput{IndexState: asset.IndexState("BOGUS")},
			wantCode:     IndexHealthUnknown,
			wantSeverity: "neutral",
			wantTerminal: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveIndexHealth(tc.input)
			assert.Equal(t, string(tc.wantCode), got.Code)
			assert.Equal(t, tc.wantSeverity, got.Severity)
			assert.Equal(t, tc.wantTerminal, got.Terminal)
			assert.Equal(t, tc.wantRetryable, got.Retryable)
		})
	}
}

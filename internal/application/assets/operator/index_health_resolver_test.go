package operator

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestResolveIndexHealth_MapsAllCases(t *testing.T) {
	cases := []struct {
		name        string
		input       IndexHealthInput
		wantCode    IndexHealthCode
		wantLabel   string
		wantSeverit string
		wantTermin  bool
		wantRetryab bool
	}{
		{
			name:      "NOT_INDEXABLE maps to NOT_INDEXABLE",
			input:     IndexHealthInput{IndexState: asset.StateNotIndexable},
			wantCode:  IndexHealthNotIndexable,
			wantLabel: "Non indicizzabile", wantSeverit: "neutral",
			wantTermin: true, wantRetryab: false,
		},
		{
			name:      "DISCOVERED maps to PENDING",
			input:     IndexHealthInput{IndexState: asset.StateDiscovered},
			wantCode:  IndexHealthPending,
			wantLabel: "In attesa", wantSeverit: "warning",
			wantTermin: false, wantRetryab: false,
		},
		{
			name:      "legacy INDEX_PENDING maps to PENDING",
			input:     IndexHealthInput{IndexState: asset.StateIndexPending},
			wantCode:  IndexHealthPending,
			wantLabel: "In attesa", wantSeverit: "warning",
			wantTermin: false, wantRetryab: false,
		},
		{
			name:      "EMBEDDING maps to EMBEDDING",
			input:     IndexHealthInput{IndexState: asset.StateEmbedding},
			wantCode:  IndexHealthEmbedding,
			wantLabel: "Embedding in corso", wantSeverit: "info",
			wantTermin: false, wantRetryab: false,
		},
		{
			name:      "EMBEDDED without pending outbox maps to INDEXING",
			input:     IndexHealthInput{IndexState: asset.StateEmbedded},
			wantCode:  IndexHealthIndexing,
			wantLabel: "Indicizzazione in corso", wantSeverit: "info",
			wantTermin: false, wantRetryab: false,
		},
		{
			name: "EMBEDDED with pending outbox maps to PENDING",
			input: IndexHealthInput{
				IndexState:          asset.StateEmbedded,
				PendingOutboxEvents: 2,
			},
			wantCode:  IndexHealthPending,
			wantLabel: "In attesa", wantSeverit: "warning",
			wantTermin: false, wantRetryab: false,
		},
		{
			name:      "INDEXING maps to INDEXING",
			input:     IndexHealthInput{IndexState: asset.StateIndexing},
			wantCode:  IndexHealthIndexing,
			wantLabel: "Indicizzazione in corso", wantSeverit: "info",
			wantTermin: false, wantRetryab: false,
		},
		{
			name: "INDEXED with matching hashes maps to INDEXED",
			input: IndexHealthInput{
				IndexState:         asset.StateIndexed,
				ContentHash:        "hash-1",
				IndexedContentHash: "hash-1",
			},
			wantCode:  IndexHealthIndexed,
			wantLabel: "Indicizzato", wantSeverit: "success",
			wantTermin: true, wantRetryab: false,
		},
		{
			name: "INDEXED with mismatched hashes maps to STALE",
			input: IndexHealthInput{
				IndexState:         asset.StateIndexed,
				ContentHash:        "hash-1-new",
				IndexedContentHash: "hash-1-old",
			},
			wantCode:  IndexHealthStale,
			wantLabel: "Stale", wantSeverit: "warning",
			wantTermin: false, wantRetryab: true,
		},
		{
			name:      "EMBEDDING_FAILED maps to FAILED",
			input:     IndexHealthInput{IndexState: asset.StateEmbeddingFailed},
			wantCode:  IndexHealthFailed,
			wantLabel: "Fallito", wantSeverit: "error",
			wantTermin: true, wantRetryab: true,
		},
		{
			name:      "INDEXING_FAILED maps to FAILED",
			input:     IndexHealthInput{IndexState: asset.StateIndexingFailed},
			wantCode:  IndexHealthFailed,
			wantLabel: "Fallito", wantSeverit: "error",
			wantTermin: true, wantRetryab: true,
		},
		{
			name:      "legacy INDEX_FAILED maps to FAILED",
			input:     IndexHealthInput{IndexState: asset.StateIndexFailed},
			wantCode:  IndexHealthFailed,
			wantLabel: "Fallito", wantSeverit: "error",
			wantTermin: true, wantRetryab: true,
		},
		{
			name:      "INDEXING_SKIPPED_NO_INDEXER maps to RETRY_WAIT",
			input:     IndexHealthInput{IndexState: asset.StateIndexingSkippedNoIndexer},
			wantCode:  IndexHealthRetryWait,
			wantLabel: "Attesa retry", wantSeverit: "warning",
			wantTermin: false, wantRetryab: true,
		},
		{
			name:      "DELETE_PENDING maps to DELETED",
			input:     IndexHealthInput{IndexState: asset.StateIndexDeletePending},
			wantCode:  IndexHealthDeleted,
			wantLabel: "Eliminato", wantSeverit: "neutral",
			wantTermin: true, wantRetryab: false,
		},
		{
			name:      "DELETED maps to DELETED",
			input:     IndexHealthInput{IndexState: asset.StateDELETED},
			wantCode:  IndexHealthDeleted,
			wantLabel: "Eliminato", wantSeverit: "neutral",
			wantTermin: true, wantRetryab: false,
		},
		{
			name:      "unrecognized state maps to UNKNOWN",
			input:     IndexHealthInput{IndexState: asset.IndexState("weird_state")},
			wantCode:  IndexHealthUnknown,
			wantLabel: "Sconosciuto", wantSeverit: "neutral",
			wantTermin: false, wantRetryab: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveIndexHealth(tc.input)
			if got.Code != string(tc.wantCode) {
				t.Fatalf("Code: got %q, want %q", got.Code, tc.wantCode)
			}
			if got.Label != tc.wantLabel {
				t.Fatalf("Label: got %q, want %q", got.Label, tc.wantLabel)
			}
			if got.Severity != tc.wantSeverit {
				t.Fatalf("Severity: got %q, want %q", got.Severity, tc.wantSeverit)
			}
			if got.Terminal != tc.wantTermin {
				t.Fatalf("Terminal: got %v, want %v", got.Terminal, tc.wantTermin)
			}
			if got.Retryable != tc.wantRetryab {
				t.Fatalf("Retryable: got %v, want %v", got.Retryable, tc.wantRetryab)
			}
		})
	}
}

func TestResolveIndexHealth_StaleEdgeCases(t *testing.T) {
	base := IndexHealthInput{
		IndexState: asset.StateIndexed,
	}

	// Empty content hash: not stale even if indexed hash differs.
	input := base
	input.ContentHash = ""
	input.IndexedContentHash = "something"
	got := ResolveIndexHealth(input)
	if got.Code != string(IndexHealthIndexed) {
		t.Fatalf("expected INDEXED when content hash is empty, got %s", got.Code)
	}

	// Content hash present but indexed hash empty: stale.
	input = base
	input.ContentHash = "hash-1"
	input.IndexedContentHash = ""
	got = ResolveIndexHealth(input)
	if got.Code != string(IndexHealthStale) {
		t.Fatalf("expected STALE when indexed hash is empty, got %s", got.Code)
	}
}

func TestResolveIndexHealth_FailedPreservesLastError(t *testing.T) {
	input := IndexHealthInput{
		IndexState: asset.StateEmbeddingFailed,
		LastError:  "embedding model timeout",
	}
	got := ResolveIndexHealth(input)
	if got.Code != string(IndexHealthFailed) {
		t.Fatalf("expected FAILED, got %s", got.Code)
	}
	if got.Description != "embedding model timeout" {
		t.Fatalf("expected last error in description, got %q", got.Description)
	}
}

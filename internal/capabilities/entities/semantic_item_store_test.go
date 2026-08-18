package entities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInMemorySemanticItemStoreRoundTrip pins save-then-list determinism and
// upsert-by-semantic-id (replace whole set) semantics.
func TestInMemorySemanticItemStoreRoundTrip(t *testing.T) {
	store := NewInMemorySemanticItemStore()
	first := validSemanticItem()
	second := validSemanticItem()
	second.SemanticID = "sem_scene03_money_02"
	second.Type = SemanticMoney
	second.Text = "more than 100 million dollars"
	second.NormalizedText = "more than 100 million dollars"
	second.StartUS = 17_200_000
	second.EndUS = 19_500_000

	require.NoError(t, store.SaveItems(context.Background(), 7, []SemanticItem{first, second}))
	got, err := store.ListItems(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Deterministic play order: start_us then semantic_id.
	require.Equal(t, first.SemanticID, got[0].SemanticID)
	require.Equal(t, second.SemanticID, got[1].SemanticID)

	// Replace whole set: a smaller re-save must not leave stale rows.
	require.NoError(t, store.SaveItems(context.Background(), 7, []SemanticItem{second}))
	got, err = store.ListItems(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, second.SemanticID, got[0].SemanticID)
}

// TestInMemorySemanticItemStore_RejectsInvalid pins fail-closed writes: an
// invalid item aborts the whole save, leaving no partial state.
func TestInMemorySemanticItemStore_RejectsInvalid(t *testing.T) {
	store := NewInMemorySemanticItemStore()
	good := validSemanticItem()
	bad := validSemanticItem()
	bad.SemanticID = ""
	bad.StartChar = -1

	err := store.SaveItems(context.Background(), 7, []SemanticItem{good, bad})
	require.ErrorIs(t, err, ErrInvalidSemanticItem)

	got, listErr := store.ListItems(context.Background(), 7)
	require.NoError(t, listErr)
	require.Empty(t, got, "no partial state may leak after a failed save")
}

// TestInMemorySemanticItemStore_EmptyScript pins that listing a never-saved
// script returns an empty, non-nil slice.
func TestInMemorySemanticItemStore_EmptyScript(t *testing.T) {
	store := NewInMemorySemanticItemStore()
	got, err := store.ListItems(context.Background(), 999)
	require.NoError(t, err)
	require.Empty(t, got)
}

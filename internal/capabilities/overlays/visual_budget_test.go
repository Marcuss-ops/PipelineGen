package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultVisualBudget(t *testing.T) {
	b := DefaultVisualBudget("scene_04")
	require.Equal(t, "scene_04", b.SceneID)
	require.Equal(t, 2, b.MaxEntityImages)
	require.Equal(t, 2, b.MaxTextCallouts)
	require.Equal(t, 1, b.MaxNumberCards)
	require.Equal(t, 4, b.MaxOverlaysTotal)
}

// TestVisualBudget_ApplyEnforcesPerKindAndTotal pins the canonical drop: a
// third entity image is dropped (its cap is full), and the total cap also
// bounds the survivors.
func TestVisualBudget_ApplyEnforcesPerKindAndTotal(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "b", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "c", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "d", Kind: IntentKindImportantText, Priority: 100},
		{IntentID: "e", Kind: IntentKindImportantNumber, Priority: 90},
	}
	got := DefaultVisualBudget("scene_04").Apply(intents)

	ids := intentIDs(got)
	// c (the third entity image) is dropped; survivors keep original order.
	require.Equal(t, []string{"a", "b", "d", "e"}, ids)
}

// TestVisualBudget_ApplyTotalCapDropsLowestPriority pins that the total cap
// drops the lowest-priority intents regardless of kind.
func TestVisualBudget_ApplyTotalCapDropsLowestPriority(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "low", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "high", Kind: IntentKindImportantText, Priority: 100},
		{IntentID: "mid", Kind: IntentKindImportantNumber, Priority: 90},
	}
	budget := VisualBudget{SceneID: "s", MaxOverlaysTotal: 2} // per-kind unlimited (0)
	got := budget.Apply(intents)
	require.Equal(t, []string{"high", "mid"}, intentIDs(got))
}

// TestVisualBudget_ApplyPerKindCapsIndependent pins that each kind cap is
// enforced on its own bucket (an entity image never eats a number-card slot).
func TestVisualBudget_ApplyPerKindCapsIndependent(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "img1", Kind: IntentKindEntityImage, Priority: 90},
		{IntentID: "img2", Kind: IntentKindEntityImage, Priority: 90},
		{IntentID: "num1", Kind: IntentKindImportantNumber, Priority: 50},
		{IntentID: "txt1", Kind: IntentKindImportantText, Priority: 50},
	}
	budget := VisualBudget{
		SceneID:          "s",
		MaxEntityImages:  1,
		MaxTextCallouts:  2,
		MaxNumberCards:   2,
		MaxOverlaysTotal: 10,
	}
	got := budget.Apply(intents)
	// Only the second entity image is dropped; the number card and text callout
	// each fit their own buckets.
	require.Equal(t, []string{"img1", "num1", "txt1"}, intentIDs(got))
}

// TestVisualBudget_ApplyZeroCapsUnlimited pins that a zero cap means "no cap"
// (the zero-value budget is a safe no-op).
func TestVisualBudget_ApplyZeroCapsUnlimited(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "b", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "c", Kind: IntentKindImportantText, Priority: 100},
	}
	var budget VisualBudget // all zero
	got := budget.Apply(intents)
	require.Equal(t, []string{"a", "b", "c"}, intentIDs(got))
}

func TestVisualBudget_ApplyDeterministic(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "b", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "c", Kind: IntentKindEntityImage, Priority: 80},
		{IntentID: "d", Kind: IntentKindImportantText, Priority: 100},
	}
	budget := DefaultVisualBudget("s")
	first := intentIDs(budget.Apply(intents))
	for i := 0; i < 100; i++ {
		require.Equal(t, first, intentIDs(budget.Apply(intents)))
	}
}

func TestVisualBudget_ApplyEmpty(t *testing.T) {
	require.Nil(t, DefaultVisualBudget("s").Apply(nil))
	require.Empty(t, DefaultVisualBudget("s").Apply([]VisualIntent{}))
}

func TestVisualBudget_Validate(t *testing.T) {
	require.NoError(t, DefaultVisualBudget("scene_04").Validate())
	require.Error(t, VisualBudget{SceneID: ""}.Validate())
	require.Error(t, VisualBudget{SceneID: "s", MaxEntityImages: -1}.Validate())
}

func TestIntentBudgetBucket(t *testing.T) {
	require.Equal(t, bucketEntityImages, intentBudgetBucket(IntentKindEntityImage))
	require.Equal(t, bucketNumberCards, intentBudgetBucket(IntentKindImportantNumber))
	require.Equal(t, bucketTextCallouts, intentBudgetBucket(IntentKindImportantText))
	require.Equal(t, bucketTextCallouts, intentBudgetBucket(IntentKindQuoteCard))
	require.Equal(t, bucketTextCallouts, intentBudgetBucket(IntentKindDateBadge))
	require.Equal(t, bucketTextCallouts, intentBudgetBucket(IntentKindLocationCard))
}

func intentIDs(intents []VisualIntent) []string {
	out := make([]string, len(intents))
	for i, it := range intents {
		out[i] = it.IntentID
	}
	return out
}

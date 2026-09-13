package persistence

import (
	"context"
	"testing"
)

// retiringCommitter exposes ONLY the retirement method on top of the committer
// interface. Resolution is structural, so a committer that answers
// SoftDeleteAsset must be found without naming its concrete type.
type retiringCommitter struct {
	AssetCommitter
	calls int
}

func (c *retiringCommitter) SoftDeleteAsset(context.Context, string) error {
	c.calls++
	return nil
}

// nonRetiringCommitter embeds the committer interface — so it satisfies
// AssetCommitter with no hand-written method set — while exposing NO retirement
// method. It models the media-disabled / pre-cutover committer, and resolution
// must report that plainly instead of inventing an engine.
type nonRetiringCommitter struct {
	AssetCommitter
}

// TestCanonicalAssetSoftDeleter_ResolvesStructuralRetirement is the positive
// case: the retirement surface behind a committer is resolved by method set.
func TestCanonicalAssetSoftDeleter_ResolvesStructuralRetirement(t *testing.T) {
	committer := &retiringCommitter{}
	resolved := CanonicalAssetSoftDeleter(committer)
	if resolved == nil {
		t.Fatal("CanonicalAssetSoftDeleter must resolve a committer exposing SoftDeleteAsset")
	}
	if err := resolved.SoftDeleteAsset(context.Background(), "asset-1"); err != nil {
		t.Fatalf("SoftDeleteAsset: %v", err)
	}
	if committer.calls != 1 {
		t.Errorf("calls = %d, want 1 (the resolved port must be the committer itself, not a copy)", committer.calls)
	}
}

// TestCanonicalAssetSoftDeleter_FailsClosed pins the negative cases. nil and a
// committer that cannot retire both resolve to nil, because the caller must
// fail closed rather than select an engine of its own — and the previous SQLite
// fallback is exactly what this port exists to make unrepresentable.
func TestCanonicalAssetSoftDeleter_FailsClosed(t *testing.T) {
	if got := CanonicalAssetSoftDeleter(nil); got != nil {
		t.Errorf("nil committer must resolve to nil, got %#v", got)
	}
	if got := CanonicalAssetSoftDeleter(nonRetiringCommitter{}); got != nil {
		t.Errorf("a committer without SoftDeleteAsset must resolve to nil, got %#v", got)
	}
}

// TestAssetSoftDeleter_IsNarrow is a compile-time pin on Pattern 0: the
// retirement port has exactly one method, so a caller that only retires an
// asset cannot reach the other fifteen mutations by accident. Widening the
// interface is a deliberate act, not an incidental one.
func TestAssetSoftDeleter_IsNarrow(t *testing.T) {
	var _ AssetSoftDeleter = &retiringCommitter{}
	var d AssetSoftDeleter = &retiringCommitter{}
	if err := d.SoftDeleteAsset(context.Background(), "x"); err != nil {
		t.Fatalf("SoftDeleteAsset: %v", err)
	}
}

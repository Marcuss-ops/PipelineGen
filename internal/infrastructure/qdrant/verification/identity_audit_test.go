package verification

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// fakePager is a canned ScrollPager that returns pre-baked pages in order,
// then terminates with an empty NextOffset.
type fakePager struct {
	pages []*schema.ScrollResult
	err   error
	idx   int
}

func (f *fakePager) ScrollPoints(_ context.Context, _ string, _ string, _ int, _ map[string]any) (*schema.ScrollResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.idx >= len(f.pages) {
		return &schema.ScrollResult{}, nil
	}
	page := f.pages[f.idx]
	f.idx++
	// Drive pagination: any page followed by another page carries a
	// non-empty NextOffset so the audit loop keeps scrolling.
	if page.NextOffset == "" && f.idx < len(f.pages) {
		page.NextOffset = "next"
	}
	return page, nil
}

func scrollPage(points ...schema.ScrollPoint) *schema.ScrollResult {
	return &schema.ScrollResult{Points: points}
}

func point(id, assetID string) schema.ScrollPoint {
	return schema.ScrollPoint{ID: id, Payload: map[string]any{"asset_id": assetID}}
}

func TestCountDuplicateAssetPoints_CleanIsZero(t *testing.T) {
	pager := &fakePager{pages: []*schema.ScrollResult{
		scrollPage(point("p1", "asset-a"), point("p2", "asset-b")),
	}}
	got, err := CountDuplicateAssetPoints(context.Background(), pager, "media_assets")
	if err != nil {
		t.Fatalf("CountDuplicateAssetPoints: %v", err)
	}
	if got != 0 {
		t.Fatalf("duplicates = %d, want 0", got)
	}
}

func TestCountDuplicateAssetPoints_DetectsDuplicatesAcrossPages(t *testing.T) {
	// asset-a appears once on page 1 and twice on page 2 → 2 extra points.
	pager := &fakePager{pages: []*schema.ScrollResult{
		scrollPage(point("p1", "asset-a"), point("p2", "asset-b")),
		scrollPage(point("p3", "asset-a"), point("p4", "asset-a")),
	}}
	got, err := CountDuplicateAssetPoints(context.Background(), pager, "media_assets")
	if err != nil {
		t.Fatalf("CountDuplicateAssetPoints: %v", err)
	}
	if got != 2 {
		t.Fatalf("duplicates = %d, want 2", got)
	}
}

func TestCountDuplicateAssetPoints_CountsExtraPointsPerAsset(t *testing.T) {
	// asset-a: 3 points → 2 duplicates; asset-b: 2 points → 1 duplicate.
	pager := &fakePager{pages: []*schema.ScrollResult{
		scrollPage(
			point("p1", "asset-a"),
			point("p2", "asset-a"),
			point("p3", "asset-a"),
			point("p4", "asset-b"),
			point("p5", "asset-b"),
		),
	}}
	got, err := CountDuplicateAssetPoints(context.Background(), pager, "media_assets")
	if err != nil {
		t.Fatalf("CountDuplicateAssetPoints: %v", err)
	}
	if got != 3 {
		t.Fatalf("duplicates = %d, want 3 (2 for asset-a + 1 for asset-b)", got)
	}
}

func TestCountDuplicateAssetPoints_IgnoresMissingAssetID(t *testing.T) {
	pager := &fakePager{pages: []*schema.ScrollResult{
		scrollPage(
			schema.ScrollPoint{ID: "p1", Payload: map[string]any{}},
			schema.ScrollPoint{ID: "p2", Payload: map[string]any{"asset_id": 42}}, // non-string
			point("p3", "asset-a"),
			point("p4", "asset-a"),
		),
	}}
	got, err := CountDuplicateAssetPoints(context.Background(), pager, "media_assets")
	if err != nil {
		t.Fatalf("CountDuplicateAssetPoints: %v", err)
	}
	if got != 1 {
		t.Fatalf("duplicates = %d, want 1 (only asset-a)", got)
	}
}

func TestCountDuplicateAssetPoints_ScrollErrorFailsClosed(t *testing.T) {
	pager := &fakePager{pages: []*schema.ScrollResult{
		scrollPage(point("p1", "asset-a")),
	}, err: errors.New("boom")}
	_, err := CountDuplicateAssetPoints(context.Background(), pager, "media_assets")
	if err == nil {
		t.Fatal("want non-nil error on scroll failure")
	}
}

func TestCountDuplicateAssetPoints_NilPagerFailsClosed(t *testing.T) {
	if _, err := CountDuplicateAssetPoints(context.Background(), nil, "media_assets"); err == nil {
		t.Fatal("want non-nil error on nil pager")
	}
}

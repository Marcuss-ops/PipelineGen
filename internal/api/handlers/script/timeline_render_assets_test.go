package script

import (
	"strings"
	"testing"

	"velox/go-master/internal/media/association"
)

func TestRenderSegmentAssetsRendersBothStockAndArtlistBlocks(t *testing.T) {
	seg := TimelineSegment{
		StockMatches: []association.ScoredMatch{
			{Title: "Stock Clip", Link: "https://drive.google.com/file/d/stock/view", Score: 90, Source: "drive_stock"},
		},
		ArtlistMatches: []association.ScoredMatch{
			{Title: "Artlist Clip", Link: "https://drive.google.com/file/d/artlist/view", Score: 88, Source: "artlist_stock"},
		},
	}

	out := renderSegmentAssets(seg)

	if !strings.Contains(out, "📦 Stock Drive Association") {
		t.Fatalf("expected stock association block, got:\n%s", out)
	}
	if !strings.Contains(out, "📦 Artlist Drive Association") {
		t.Fatalf("expected artlist association block, got:\n%s", out)
	}
	if !strings.Contains(out, "Stock Clip") || !strings.Contains(out, "Artlist Clip") {
		t.Fatalf("expected both clip titles in output, got:\n%s", out)
	}
}


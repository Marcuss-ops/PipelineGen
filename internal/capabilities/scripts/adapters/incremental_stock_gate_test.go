package adapters

import (
	"context"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stockGatePlan is the Milton mixed shape: the caller's stock bindings ride
// the resolved plan exactly as expandSegmentStockFolders + BuildGenerateRequest
// + buildPlan produce them in production.
func stockGatePlan() *scriptpkg.ResolvedGenerationPlan {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "mixed", Language: "pt",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled,
			},
		},
		StockBindings: []scriptpkg.StockBindingInput{{
			Index: 1, SceneID: "scene-1", SegmentID: "scene-1",
			FolderID: "stock-folder", FolderLink: "https://drive.google.com/drive/folders/stock-folder", Source: "drive",
		}},
	}
	return plan
}

func stockGateSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", SceneID: "scene-1", Position: 1, Text: "stock scene", TextHash: "hash-stock",
		Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"stock image query"}, ArtlistQueries: []string{"stock clip query"}},
	}
}

// TestIncrementalFanoutStockBoundSegmentNeverSearches covers the
// SemanticAndFanoutResolver / VidRushProviderFanout production path: a
// stock-bound segment must leave provider discovery with BYPASSED markers and
// no candidates, while the same segment WITHOUT the binding keeps searching.
func TestIncrementalFanoutStockBoundSegmentNeverSearches(t *testing.T) {
	artlist := &stockGateArtlistSearcher{}
	images := &stockGateImageSearcher{}
	fanout := NewVidRushProviderFanout(artlist, images)

	plan := stockGatePlan()
	got, err := fanout.ResolveProviders(context.Background(), plan, stockGateSegment())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cache.Artlist != "BYPASSED" || got.Cache.InternetImages != "BYPASSED" || got.Cache.Binding != "STOCK_BOUND" {
		t.Fatalf("stock-bound fanout cache state = %+v, want BYPASSED + STOCK_BOUND", got.Cache)
	}
	if len(got.Assets.Candidates) != 0 || len(got.Assets.SecondaryImages) != 0 {
		t.Fatalf("stock-bound fanout produced provider media: %+v", got.Assets)
	}
	if len(artlist.queries) != 0 || len(images.queries) != 0 {
		t.Fatalf("stock-bound segment reached the searchers: artlist=%v images=%v", artlist.queries, images.queries)
	}

	// Control: the identical segment WITHOUT the binding must search.
	freePlan := stockGatePlan()
	freePlan.StockBindings = nil
	free, err := fanout.ResolveProviders(context.Background(), freePlan, stockGateSegment())
	if err != nil {
		t.Fatal(err)
	}
	if free.Cache.InternetImages == "BYPASSED" || len(free.Assets.SecondaryImages) == 0 {
		t.Fatalf("control segment without binding did not search: cache=%+v images=%d", free.Cache, len(free.Assets.SecondaryImages))
	}
	if !containsQuery(images.queries, "stock image query") {
		t.Fatalf("control segment query never reached the image searcher: %v", images.queries)
	}
}

// TestMaterializePortStockBoundSegmentNeverAcquires covers the single-segment
// Materialize port the incremental coordinator drives: the plan is the only
// binding surface and the stock-bound segment must return untouched.
func TestMaterializePortStockBoundSegmentNeverAcquires(t *testing.T) {
	processor := NewVidRushMaterializationProcessor(nil, nil)
	got, err := processor.Materialize(context.Background(), stockGatePlan(), stockGateSegment())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cache.Binding != "STOCK_BOUND" || got.Cache.InternetImages != "BYPASSED" {
		t.Fatalf("Materialize cache state = %+v, want STOCK_BOUND + BYPASSED", got.Cache)
	}
	if len(got.Assets.Candidates) != 0 || len(got.Assets.SecondaryImages) != 0 {
		t.Fatalf("Materialize acquired provider media for a stock-bound segment: %+v", got.Assets)
	}
}

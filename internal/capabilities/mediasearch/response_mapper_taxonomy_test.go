package mediasearch

import (
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// TestResultToResponseCarriesTaxonomy pins that the asset FAMILY and usage
// intent reach the client, so a caller can ask for (and verify) "YouTube-native
// clips" without parsing the asset-id prefix.
func TestResultToResponseCarriesTaxonomy(t *testing.T) {
	r := &search.Result{
		Items: []search.Candidate{
			{AssetID: "yt_native", Source: "youtube", Title: "Press conference", Score: 0.9},
			{AssetID: "planner:1", Source: "youtube", AssetKind: "stock_video", SemanticRole: "stock", Title: "Mike Tyson stock library", Score: 0.8},
		},
	}
	resp := resultToResponse(r, "mike", search.SearchModeHybrid, search.SearchCatalog, "")
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	if resp.Items[1].AssetKind != "stock_video" || resp.Items[1].SemanticRole != "stock" {
		t.Fatalf("item taxonomy = %q/%q, want stock_video/stock", resp.Items[1].AssetKind, resp.Items[1].SemanticRole)
	}
	if resp.Items[0].AssetKind != "" {
		t.Fatalf("native clip taxonomy = %q, want empty (unknown family)", resp.Items[0].AssetKind)
	}
}

package finaljob

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// NeedsStockWindowPreparation reports whether the request needs the Master to
// materialize stock windows before the final render. It is the single source of
// truth for the stock-ownership boundary.
//
// The Producer never downloads, probes, hashes, cuts, re-encodes or re-uploads
// stock bytes: stockpipeline is the only producer of stock clips, and this path
// carries metadata and velox-drive:// references only. The complete final video
// is always assembled by the Master; this package must not concatenate it on
// this host.
func NeedsStockWindowPreparation(req contract.GenerateRequest) bool {
	if len(req.StockBindings) > 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(string(req.MediaMode))) {
	case "stock_only", "mixed":
		return true
	default:
		return false
	}
}

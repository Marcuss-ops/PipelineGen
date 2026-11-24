package finaljob

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// RenderReceipt is the narrow render-side view of a run: the two overlay
// render lineages (Items and Batches) plus the subtitle policy that decides
// whether fixed media must be re-rendered. Consumers that only need these
// facts never receive the whole GenerateResult.
type RenderReceipt struct {
	Batches   []contract.OverlayBatchRenderReference
	Items     []contract.OverlayItemRenderReference
	Subtitles *scriptpkg.VideoSubtitlesSpec
}

// RenderReceiptFor projects the render receipt without copying the payload
// slices. Nil result or nil OverlayRender is an empty receipt, not an error.
func RenderReceiptFor(req contract.GenerateRequest, result *contract.GenerateResult) RenderReceipt {
	receipt := RenderReceipt{Subtitles: req.Render.Subtitles}
	if result != nil && result.OverlayRender != nil {
		receipt.Batches = result.OverlayRender.Batches
		receipt.Items = result.OverlayRender.Items
	}
	return receipt
}

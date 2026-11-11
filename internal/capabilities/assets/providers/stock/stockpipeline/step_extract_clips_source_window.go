package stockpipeline

import (
	"fmt"
	"math"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// stagedSourceCutOffset selects the seek basis from the staged artifact's
// verified shape. A section file uses the planned section start as its local
// zero; a full source carrying SourceWindow uses absolute source timestamps.
func stagedSourceCutOffset(staged *assets.StagedAsset, plans []ClipPlan, sectionOffsetSec float64) (float64, error) {
	if staged == nil || staged.SourceWindow == nil {
		return sectionOffsetSec, nil
	}
	window := staged.SourceWindow
	if err := window.Validate(); err != nil {
		return 0, fmt.Errorf("%w: invalid source identity: %v", ErrStockStagedSourceWindowMismatch, err)
	}
	if staged.Bytes != window.SizeBytes {
		return 0, fmt.Errorf("%w: staged bytes=%d window bytes=%d", ErrStockStagedSourceWindowMismatch, staged.Bytes, window.SizeBytes)
	}
	startUS, endUS, ok := sectionWindowForPlans(plans)
	if !ok {
		return 0, fmt.Errorf("%w: plans do not form one contiguous section", ErrStockStagedSourceWindowMismatch)
	}
	// sectionDownloadString serializes boundaries to millisecond precision,
	// rounding to nearest millisecond. Compare against that exact wire window.
	wantStartUS := int64(math.Round(startUS*1_000)) * 1_000
	wantEndUS := int64(math.Round(endUS*1_000)) * 1_000
	if window.StartUS != wantStartUS || window.EndUS != wantEndUS {
		return 0, fmt.Errorf("%w: staged=[%d,%d) planned=[%d,%d)", ErrStockStagedSourceWindowMismatch, window.StartUS, window.EndUS, wantStartUS, wantEndUS)
	}
	return 0, nil
}

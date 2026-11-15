package overlays

import (
	"context"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// MapOverlapResolver compiles map/card timing collisions in one batch.
// PipelineGen owns the complete overlay DTO; the resolver returns only edits.
type MapOverlapResolver interface {
	ResolveMapOverlaps(ctx context.Context, items []capmodel.OverlayItem, sceneWindows map[string][2]int64, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error)
}

// MapWindowTrimmer applies the Rust map-window pass before editorial budgets.
type MapWindowTrimmer interface {
	TrimMapWindowsBeforeBudget(ctx context.Context, items []capmodel.OverlayItem, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error)
}

// EntranceCascadeResolver applies the pure, frame-aware entrance stagger in BrainRust.
type EntranceCascadeResolver interface {
	CascadeClusteredEntrances(ctx context.Context, items []capmodel.OverlayItem, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error)
}

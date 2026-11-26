package finaljob

import (
	"os"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// OverlayItemCandidate is one renderable overlay item together with its
// position in the sealed plan. It is local to the overlay manifest
// projection: the render queue declares its own copy because the two
// packages never exchange values.
type OverlayItemCandidate struct {
	Index  int
	Source capmodel.OverlayItem
}

// usableLocalAssetPath reports whether a path points at a real local file the
// Master can upload.
func usableLocalAssetPath(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

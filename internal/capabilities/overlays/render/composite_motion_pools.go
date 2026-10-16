package render

import capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"

// Composite image groups are shown as one overlay with 2 to 5 images placed
// side by side. Each group size has its own entrance pool, so a 2-image group,
// a 3-image group, and so on never share the same entrance family and each
// group reads as its own sequence, the way text and entity-caption treatments
// already do.
const (
	minCompositeGroupSize = 2
	maxCompositeGroupSize = 5
)

// CompositeImageMotionPool returns the certified entrance pool reserved for a
// composite group of size images. The pools are a deterministic, disjoint
// partition of the certified entity image catalog by position: entry i belongs
// to size minCompositeGroupSize + (i mod 4). Sizes outside 2..5 return the full
// catalog. When the catalog has fewer entries than sizes, every size shares the
// full catalog (there is nothing to partition).
func CompositeImageMotionPool(size int) []string {
	catalog := capoverlay.CertifiedEntityImageMotions()
	if size < minCompositeGroupSize || size > maxCompositeGroupSize {
		return catalog
	}
	groupCount := maxCompositeGroupSize - minCompositeGroupSize + 1
	if len(catalog) < groupCount {
		return catalog
	}
	pool := make([]string, 0, len(catalog)/groupCount+1)
	for index, id := range catalog {
		if index%groupCount == size-minCompositeGroupSize {
			pool = append(pool, id)
		}
	}
	return pool
}

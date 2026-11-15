package render

import capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"

// Composite image groups are shown as one overlay with 2 to 5 images placed
// side by side. Each group size rotates the certified image catalog from its
// own starting point, so a 2-image group, a 3-image group, and so on read as
// their own sequence while every member of the group still gets a distinct
// entrance.
const (
	minCompositeGroupSize = 2
	maxCompositeGroupSize = 5
)

// CompositeImageMotionPool returns the certified entrance pool reserved for a
// composite group of size images. The pool is a deterministic rotation of the
// complete certified entity-image catalog, offset by group size.
//
// It deliberately does NOT slice the catalog into disjoint per-size pools: a
// 9-motion catalog cannot give 2, 3, 4 and 5-image groups two or more distinct
// entrances each (that would need 14 entries). The former partition left the
// 3-, 4- and 5-image groups with only two motions, so a larger composite
// always repeated an entrance. Rotation gives every group at least `size`
// distinct motions; the size-specific start keeps the group sizes from opening
// on the same entrance.
func CompositeImageMotionPool(size int) []string {
	catalog := capoverlay.CertifiedEntityImageMotions()
	if size < minCompositeGroupSize || size > maxCompositeGroupSize || len(catalog) == 0 {
		return catalog
	}
	offset := (size - minCompositeGroupSize) % len(catalog)
	pool := make([]string, 0, len(catalog))
	pool = append(pool, catalog[offset:]...)
	pool = append(pool, catalog[:offset]...)
	return pool
}

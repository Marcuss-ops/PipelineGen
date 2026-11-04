package entities

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"testing"
)

// findItem is the shared overlay-plan lookup formerly owned by the deleted
// certification_test.go battery. Kept because overlay_resolver_test.go uses it.
func findItem(t *testing.T, plan capmodel.OverlayPlan, id string) capmodel.OverlayItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("overlay item %q not found in the plan", id)
	return capmodel.OverlayItem{}
}

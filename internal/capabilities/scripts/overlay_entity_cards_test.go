package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestCapEntityImageOverlaysKeepsFiveDistinctIdentities(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 0, 8)
	for i, entityID := range []string{"a", "b", "a", "c", "d", "e", "f", "g"} {
		items = append(items, capabilityoverlay.OverlayItem{
			ID:   "image-" + entityID + "-" + string(rune('0'+i)),
			Kind: string(capabilityoverlay.KindEntityImage), EntityID: entityID,
		})
	}
	items = append(items, capabilityoverlay.OverlayItem{ID: "phrase", Kind: "text_phrase"})

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 6 {
		t.Fatalf("items=%d, want five image items plus phrase", len(got))
	}
	seen := map[string]bool{}
	for _, item := range got {
		if item.Kind == string(capabilityoverlay.KindEntityImage) {
			seen[item.EntityID] = true
		}
	}
	if len(seen) != capabilityoverlay.MaxEntityImageOverlaysPerRun {
		t.Fatalf("distinct image identities=%d, want %d", len(seen), capabilityoverlay.MaxEntityImageOverlaysPerRun)
	}
}

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

func TestCapEntityImageOverlaysDeduplicatesSameCanonicalEntity(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "scene-1", EntityID: "occurrence-1", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-2", EntityID: "occurrence-2", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-3", EntityID: "occurrence-3", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:evander-holyfield"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "other-sha"}}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 2 {
		t.Fatalf("items=%d, want one Tyson image plus one other entity", len(got))
	}
	if got[0].EntityRef == nil || got[0].EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("first retained image = %#v, want Mike Tyson", got[0])
	}
}

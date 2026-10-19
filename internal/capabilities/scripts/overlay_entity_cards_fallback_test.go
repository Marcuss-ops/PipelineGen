package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const fallbackTestSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// In per-scene media scope a scene without its own binding for an entity
// must still resolve the entity's verified portrait from the run-level
// fallback (first binding in scene order) instead of degrading to a
// text-only card. A scene WITH its own binding keeps priority through its
// scene-scoped key.
func TestEntityCardMediaIndexFallsBackToRunLevelBinding(t *testing.T) {
	bound := scriptpkg.AnnotatedEntity{Type: "PERSON", CanonicalName: "Andrea Sempio", Confidence: 0.9,
		Image: &scriptpkg.EntityImageBinding{
			AssetID: "asset-sempio", SHA256: fallbackTestSHA,
			PreviewURL: "https://example.com/sempio.jpg", MediaType: "image/jpeg",
		}}
	unbound := scriptpkg.AnnotatedEntity{Type: "PERSON", CanonicalName: "Andrea Sempio", Confidence: 0.8}
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-a", Annotations: &scriptpkg.SceneAnnotations{SecondaryEntities: []scriptpkg.AnnotatedEntity{bound}}},
		{ID: "scene-b", Annotations: &scriptpkg.SceneAnnotations{SecondaryEntities: []scriptpkg.AnnotatedEntity{unbound}}},
	}}
	media, canonicalByStable := entityCardMediaIndex(result, true)
	stable := capabilityentities.StableEntityID("PERSON", "Andrea Sempio")
	sceneAOverlayID := "overlay-scene-a-" + capabilityentities.SafeEntityID("Andrea Sempio")
	if canonicalByStable[sceneAOverlayID] == "" {
		t.Fatalf("scene-scoped key missing: %v", canonicalByStable)
	}
	if canonicalByStable[stable] == "" {
		t.Fatalf("run-level fallback key missing: %v", canonicalByStable)
	}
	card := capabilityoverlay.OverlayItem{
		ID:      "overlay-scene-b-" + capabilityentities.SafeEntityID("Andrea Sempio"),
		SceneID: "scene-b", EntityID: stable, TemplateID: "person_default",
		Kind:      string(capabilityoverlay.KindEntityCard),
		EntityRef: &capabilityoverlay.OverlayEntityRef{Type: "PERSON", Name: "Andrea Sempio"},
	}
	got := attachEntityCardAsset(card, media, canonicalByStable, "plan")
	if got.Kind != string(capabilityoverlay.KindEntityImage) {
		t.Fatalf("unbound scene card kind = %q, want entity_image via run-level fallback", got.Kind)
	}
	if len(got.AssetRefs) != 1 || !strings.EqualFold(got.AssetRefs[0].SHA256, fallbackTestSHA) {
		t.Fatalf("unbound scene card refs = %+v, want the bound portrait", got.AssetRefs)
	}
	// A scene with its own binding still resolves its own scene-scoped key.
	boundCard := capabilityoverlay.OverlayItem{
		ID: sceneAOverlayID, SceneID: "scene-a", EntityID: stable, TemplateID: "person_default",
		Kind:      string(capabilityoverlay.KindEntityCard),
		EntityRef: &capabilityoverlay.OverlayEntityRef{Type: "PERSON", Name: "Andrea Sempio"},
	}
	if got := attachEntityCardAsset(boundCard, media, canonicalByStable, "plan"); got.Kind != string(capabilityoverlay.KindEntityImage) {
		t.Fatalf("bound scene card kind = %q, want entity_image", got.Kind)
	}
}

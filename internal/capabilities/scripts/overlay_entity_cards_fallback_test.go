package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const fallbackTestSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// In per-scene media scope a scene without its own binding for an entity must
// still resolve the entity's verified portrait from the run-level fallback
// (first binding in scene order) instead of degrading to a text-only card. The
// planner now consumes this identity projection through
// `media.promotable_entity_assets`, so the index itself is what is pinned here.
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
	media, canonicalByStable := entityCardMediaIndex(result, overlayPlanImageOptions{PerSceneEntityCards: true})
	stable := capabilityentities.StableEntityID("PERSON", "Andrea Sempio")
	sceneAOverlayID := "overlay-scene-a-" + capabilityentities.SafeEntityID("Andrea Sempio")
	if canonicalByStable[sceneAOverlayID] == "" {
		t.Fatalf("scene-scoped key missing: %v", canonicalByStable)
	}
	if canonicalByStable[stable] == "" {
		t.Fatalf("run-level fallback key missing: %v", canonicalByStable)
	}
	ref, err := media.ResolveBest(canonicalByStable[stable])
	if err != nil {
		t.Fatalf("run-level fallback resolve: %v", err)
	}
	if !strings.EqualFold(ref.SHA256, fallbackTestSHA) {
		t.Fatalf("run-level fallback sha = %q, want the bound portrait", ref.SHA256)
	}
	// A scene with its own binding still resolves its own scene-scoped key.
	scoped, err := media.ResolveBest(canonicalByStable[sceneAOverlayID])
	if err != nil {
		t.Fatalf("scene-scoped resolve: %v", err)
	}
	if !strings.EqualFold(scoped.SHA256, fallbackTestSHA) {
		t.Fatalf("scene-scoped sha = %q, want the bound portrait", scoped.SHA256)
	}
}

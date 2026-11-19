package scriptgeneration

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func imageBearingPerson(name string) scriptpkg.AnnotatedEntity {
	return scriptpkg.AnnotatedEntity{
		CanonicalName: name,
		Type:          "PERSON",
		Image: &scriptpkg.EntityImageBinding{
			Status: "resolved", AssetID: name, SHA256: strings.Repeat("a", 64),
		},
	}
}

// TestGenericSceneImageLimitReservesEntityCardSlots pins the admission order:
// Rust admits base stills before entity cards, so a scene offering five generic
// images and three certified portraits lost all three cards to the six-overlay
// ceiling. The generic lane must leave room for the portraits.
func TestGenericSceneImageLimitReservesEntityCardSlots(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-02",
		Annotations: &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				imageBearingPerson("Mike Tyson"),
				imageBearingPerson("Evander Holyfield"),
				imageBearingPerson("Don King"),
			},
		},
	}}}
	if got := entityImageCardReservation(result, "scene-02"); got != 3 {
		t.Fatalf("reserved entity images = %d, want 3", got)
	}
	if got := genericSceneImageLimit(result, "scene-02", 6); got != 3 {
		t.Fatalf("generic still limit = %d, want 3 (6 slots - 3 portraits)", got)
	}
}

func TestGenericSceneImageLimitKeepsCertifiedLimitWithoutPortraits(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID: "scene-01",
		Annotations: &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Milano", Type: "GPE"}},
		},
	}}}
	if got := genericSceneImageLimit(result, "scene-01", 6); got != maxGenericSceneImagesPerScene {
		t.Fatalf("generic still limit = %d, want %d", got, maxGenericSceneImagesPerScene)
	}
}

func TestGenericSceneImageLimitNeverGoesNegative(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID: "crowded",
		Annotations: &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				imageBearingPerson("A"), imageBearingPerson("B"),
				imageBearingPerson("C"), imageBearingPerson("D"),
			},
		},
	}}}
	if got := genericSceneImageLimit(result, "crowded", 2); got != 0 {
		t.Fatalf("generic still limit = %d, want 0 when cards own the budget", got)
	}
}

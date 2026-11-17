package scriptgeneration

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func identityImageTestBinding(sha, url string) *scriptpkg.EntityImageBinding {
	return &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "entity-image", SHA256: sha,
		PreviewURL: url, MediaType: "image/jpeg",
	}
}

func TestHydrateLocalizedEntityImagesReusesSourcePortraitByCanonicalIdentity(t *testing.T) {
	sha := strings.Repeat("a", 64)
	result := &GenerateResult{
		SourceLanguage: "en",
		Scenes: []Scene{{
			ID: "scene-01",
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "local:abc:person_01",
				Image: identityImageTestBinding(sha, "https://drive.example/mike"),
			}}},
			LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
				"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					Type: "PERSON", CanonicalName: "Mike’a Tysona", CanonicalEntityID: "local:abc:person_01",
				}}},
			},
		}},
	}

	if got := hydrateLocalizedEntityImages(result, "pl"); got != 1 {
		t.Fatalf("hydrated = %d, want 1", got)
	}
	image := result.Scenes[0].LocalizedAnnotations["pl"].PrimaryEntities[0].Image
	if image == nil || image.SHA256 != sha || image.PreviewURL != "https://drive.example/mike" {
		t.Fatalf("localized image = %#v, want the verified source binding", image)
	}
}

func TestHydrateLocalizedEntityImagesResolvesFamilyNameAlias(t *testing.T) {
	sha := strings.Repeat("b", 64)
	result := &GenerateResult{
		SourceLanguage: "it",
		Scenes: []Scene{{
			ID: "scene-03",
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "local:abc:person_01",
				Image: identityImageTestBinding(sha, "https://drive.example/mike"),
			}}},
			LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
				"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					Type: "CONCEPT", CanonicalName: "Tyson", CanonicalEntityID: "local:abc:concept_07",
				}}},
			},
		}},
	}

	if got := hydrateLocalizedEntityImages(result, "pl"); got != 1 {
		t.Fatalf("hydrated = %d, want the family-name alias to inherit the portrait", got)
	}
	if image := result.Scenes[0].LocalizedAnnotations["pl"].PrimaryEntities[0].Image; image == nil || image.SHA256 != sha {
		t.Fatalf("alias image = %#v, want the verified source sha", image)
	}
}

func TestHydrateLocalizedEntityImagesSkipsAmbiguousFamilyName(t *testing.T) {
	sha := strings.Repeat("c", 64)
	result := &GenerateResult{
		SourceLanguage: "it",
		Scenes: []Scene{{
			ID: "scene-01",
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson",
					Image: identityImageTestBinding(sha, "https://drive.example/mike")},
				{Type: "PERSON", CanonicalName: "John Tyson", CanonicalEntityID: "person:john-tyson",
					Image: identityImageTestBinding(strings.Repeat("d", 64), "https://drive.example/john")},
			}},
			LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
				"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					Type: "CONCEPT", CanonicalName: "Tyson", CanonicalEntityID: "local:abc:concept_07",
				}}},
			},
		}},
	}

	if got := hydrateLocalizedEntityImages(result, "pl"); got != 0 {
		t.Fatalf("hydrated = %d, want 0: a shared family name must not be guessed", got)
	}
	if image := result.Scenes[0].LocalizedAnnotations["pl"].PrimaryEntities[0].Image; image != nil {
		t.Fatalf("ambiguous alias image = %#v, want nil", image)
	}
}

func TestHydrateLocalizedEntityImagesKeepsExistingVerifiedBinding(t *testing.T) {
	ownSHA := strings.Repeat("e", 64)
	result := &GenerateResult{
		SourceLanguage: "it",
		Scenes: []Scene{{
			ID: "scene-01",
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson",
				Image: identityImageTestBinding(strings.Repeat("f", 64), "https://drive.example/source"),
			}}},
			LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
				"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson",
					Image: identityImageTestBinding(ownSHA, "https://drive.example/pl"),
				}}},
			},
		}},
	}

	if got := hydrateLocalizedEntityImages(result, "pl"); got != 0 {
		t.Fatalf("hydrated = %d, want 0 when the localized binding is already verified", got)
	}
	if image := result.Scenes[0].LocalizedAnnotations["pl"].PrimaryEntities[0].Image; image == nil || image.SHA256 != ownSHA {
		t.Fatalf("localized image = %#v, want the existing localized binding preserved", image)
	}
}

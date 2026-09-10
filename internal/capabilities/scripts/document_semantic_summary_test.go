package scriptgeneration

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRenderDocumentSemanticSummaryGroupsEntitiesAndDriveMetadata(t *testing.T) {
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: []scriptpkg.SpecScene{
				{
					ID: "scene-0",
					Annotations: &scriptpkg.SceneAnnotations{
						ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "A defining moment"}},
						PrimaryEntities: []scriptpkg.AnnotatedEntity{{
							CanonicalName: "Donald Trump", Type: "PERSON", Confidence: 0.98,
							Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "person:donald-trump", DriveLink: "https://drive.google.com/file/d/trump/view", Source: "commons", License: "CC BY-SA"},
						}},
						SecondaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "United States", Type: "GPE"}},
					},
				},
			},
		},
	}

	html, err := RenderDocument(model, DocumentRenderOptions{Title: "Trump", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Entities &amp; Important Phrases",
		"A defining moment",
		"<h3>PERSON</h3>",
		"Donald Trump",
		"<h3>GPE</h3>",
		"United States",
		"https://drive.google.com/file/d/trump/view",
		"asset=person:donald-trump",
		"source=commons",
		"license=CC BY-SA",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("document missing %q: %s", want, html)
		}
	}
}

func TestRenderDocumentSemanticOverlayUsesExactPlanAndPublishesArtifact(t *testing.T) {
	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        "plan-ada",
		VideoID:       "video-ada",
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		Items: []capabilityoverlay.OverlayItem{
			{
				ID: "ada-card", SceneID: "scene-0", Kind: "entity_card",
				StartUS: 100_000, DurationUS: 5_000_000,
				TemplateID: "person_default", Text: "Ada Lovelace",
				EntityRef: &capabilityoverlay.OverlayEntityRef{
					EntityID: "ent-ada", Type: "PERSON", Name: "Ada Lovelace",
					CanonicalEntityID: "person:ada-lovelace",
				},
				AssetRefs: []capabilityoverlay.OverlayAssetRef{{
					AssetID: "ada-image", URL: "https://drive.google.com/uc?export=download&id=ada",
					SHA256: "ada-sha", MediaType: "image/jpeg",
				}},
			},
		},
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}

	out, err := RenderDocument(model, DocumentRenderOptions{
		Title: "Ada", OverlayPlan: &plan,
		Overlay: &scriptpkg.DocumentOverlayRef{JobID: "chronon-ada", URL: "https://drive.google.com/file/d/render/view", DurationUS: 5_100_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<h2>Semantic Overlay</h2>", "Ada Lovelace", "PERSON",
		"person:ada-lovelace", "00:00.100", "00:05.100", "ada-image",
		"<h2>Semantic Overlay JSON</h2>", "plan-ada", "entity_card",
		"<h2>Rendered Overlay</h2>", "chronon-ada",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("document missing %q: %s", want, out)
		}
	}
}

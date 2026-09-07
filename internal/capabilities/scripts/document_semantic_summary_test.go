package scriptgeneration

import (
	"strings"
	"testing"

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

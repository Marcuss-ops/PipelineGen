// Package scriptgeneration — document_editorial_projection_test.go certifies
// that the Fase 1 editorial product (editorial.v1 scene titles + extractive
// bullets) is actually VISIBLE on the document surface.
//
// The document is the human/operator surface, separate from the render
// pipeline, so exposing titles and bullets here cannot alter any video. The
// overlay byte-identity gates (R17/R18) therefore stay green, and these tests
// pin the exact opposite contract: the document MUST show them.
//
// Nothing may be fabricated: a scene whose title is "unavailable" shows no
// heading, and a scene with no bullets shows no list.
package scriptgeneration

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func editorialDocumentResult() *GenerateResult {
	return &GenerateResult{
		SourceLanguage: "en",
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "German industrial employment fell sharply."}},
			{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Steel production declined for a third quarter."}},
		},
		Editorial: &scriptpkg.EditorialManifest{
			SchemaVersion:  scriptpkg.EditorialManifestVersion,
			SourceLanguage: "en",
			ProfileVersion: "highlights.v1",
			Certified:      true,
			Fingerprint:    "fp-doc-test",
			Scenes: []scriptpkg.EditorialScene{
				{
					SceneID: "scene-0", Title: strptr("Industrial Employment Decline"),
					TitleStatus: "resolved", TitleSource: "topic_validated",
					Bullets: []scriptpkg.EditorialSceneBullet{
						{SentenceStart: 0, SentenceEnd: 1, Text: "Employment fell sharply across the Ruhr."},
						{SentenceStart: 1, SentenceEnd: 2, Text: "Factory orders collapsed regionally."},
					},
				},
				{
					// No reliable title and no bullets: the document must show
					// NEITHER rather than inventing "Scene 2".
					SceneID: "scene-1", TitleStatus: "unavailable",
					Bullets: nil,
				},
			},
		},
	}
}

// TestDocumentShowsEditorialTitlesAndBullets is the user-visible outcome: the
// operator opens the document and reads the scene title and its bullets.
func TestDocumentShowsEditorialTitlesAndBullets(t *testing.T) {
	result := editorialDocumentResult()
	model := modelScriptOutputForDocument(result, "en")
	require.NotNil(t, model)

	doc, err := RenderDocument(model, DocumentRenderOptions{Title: "Germany", Language: "en"})
	require.NoError(t, err)

	assert.Contains(t, doc, "<h3>Industrial Employment Decline</h3>",
		"the resolved scene title must be rendered as a heading")
	assert.Contains(t, doc, "<li>Employment fell sharply across the Ruhr.</li>",
		"the first extractive bullet must be rendered as a list item")
	assert.Contains(t, doc, "<li>Factory orders collapsed regionally.</li>",
		"the second extractive bullet must be rendered as a list item")

	// The projection carries the data too, so the machine JSON is inspectable.
	require.Equal(t, "Industrial Employment Decline", model.SpecScene.Scenes[0].Title)
	require.Len(t, model.SpecScene.Scenes[0].Bullets, 2)
}

// TestDocumentFabricatesNoTitleWhenUnavailable pins the fail-closed half: a
// scene with TitleStatus=unavailable must render no heading at all. A
// "Scene 2" fallback would be indistinguishable from an extracted title on the
// surface the operator reads.
func TestDocumentFabricatesNoTitleWhenUnavailable(t *testing.T) {
	result := editorialDocumentResult()
	model := modelScriptOutputForDocument(result, "en")

	assert.Empty(t, model.SpecScene.Scenes[1].Title, "an unavailable title must not become text")
	assert.Empty(t, model.SpecScene.Scenes[1].Bullets)

	doc, err := RenderDocument(model, DocumentRenderOptions{Title: "Germany", Language: "en"})
	require.NoError(t, err)
	assert.NotContains(t, doc, "<h3>Scene 2</h3>")
	assert.NotContains(t, doc, "<h3></h3>")
	// Exactly one editorial heading exists (scene-0 only).
	assert.Equal(t, 1, strings.Count(doc, "<h3>"), "only the resolved scene may carry a heading")
}

// TestDocumentDoesNotLeakEditorialAcrossLanguages pins the language scoping.
// The manifest is extracted in ONE language; a document rendered for another
// language must not surface foreign titles or bullets.
func TestDocumentDoesNotLeakEditorialAcrossLanguages(t *testing.T) {
	result := editorialDocumentResult()

	it := modelScriptOutputForDocument(result, "it")
	require.NotNil(t, it)
	assert.Empty(t, it.SpecScene.Scenes[0].Title, "a translated document must not show the source-language title")
	assert.Empty(t, it.SpecScene.Scenes[0].Bullets)

	doc, err := RenderDocument(it, DocumentRenderOptions{Title: "Germania", Language: "it"})
	require.NoError(t, err)
	assert.NotContains(t, doc, "Industrial Employment Decline")
	assert.NotContains(t, doc, "Employment fell sharply across the Ruhr.")
}

// TestDocumentWithoutEditorialIsByteStable proves the change is purely
// additive: a run with no editorial manifest produces the exact same document
// as before, because every new field is omitempty and renders nothing.
func TestDocumentWithoutEditorialIsByteStable(t *testing.T) {
	result := editorialDocumentResult()
	result.Editorial = nil

	model := modelScriptOutputForDocument(result, "en")
	doc, err := RenderDocument(model, DocumentRenderOptions{Title: "Germany", Language: "en"})
	require.NoError(t, err)

	assert.NotContains(t, doc, "<h3>")
	assert.NotContains(t, doc, "<ul>")
	assert.Empty(t, model.SpecScene.Scenes[0].Title)
	assert.Empty(t, model.SpecScene.Scenes[0].Bullets)
}

// TestDocumentBulletsAreHTMLEscaped proves operator-facing text cannot inject
// markup into the published document.
func TestDocumentBulletsAreHTMLEscaped(t *testing.T) {
	result := editorialDocumentResult()
	result.Editorial.Scenes[0].Title = strptr("Steel & <Energy> \"Costs\"")
	result.Editorial.Scenes[0].Bullets = []scriptpkg.EditorialSceneBullet{
		{SentenceStart: 0, SentenceEnd: 1, Text: "Gas <b>prices</b> spiked & margins fell."},
	}

	model := modelScriptOutputForDocument(result, "en")
	doc, err := RenderDocument(model, DocumentRenderOptions{Title: "Germany", Language: "en"})
	require.NoError(t, err)

	assert.Contains(t, doc, "<h3>Steel &amp; &lt;Energy&gt; &#34;Costs&#34;</h3>")
	assert.Contains(t, doc, "<li>Gas &lt;b&gt;prices&lt;/b&gt; spiked &amp; margins fell.</li>")
	assert.NotContains(t, doc, "<li>Gas <b>prices</b> spiked")
}

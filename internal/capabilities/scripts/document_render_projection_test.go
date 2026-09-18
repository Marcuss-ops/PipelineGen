package scriptgeneration

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// TestDocumentEditingAssetsPolicyHasBuildParityWithTheRemotePayload pins the
// build-parity contract for the policy seam: the human Google Doc block and the
// sealed remote payload must carry THE SAME policy document, because both are
// built from the single builder (remoteEditingAssetsPolicy). A second, doc-only
// policy would be exactly the duplicated catalog this consolidation removed.
func TestDocumentEditingAssetsPolicyHasBuildParityWithTheRemotePayload(t *testing.T) {
	doc, err := RenderDocument(&scriptpkg.ModelScriptOutputV1{}, DocumentRenderOptions{})
	require.NoError(t, err)

	const marker = "<h2>Editing Assets Policy JSON</h2><pre><code>"
	start := strings.Index(doc, marker)
	require.GreaterOrEqual(t, start, 0, "the document must publish the editing-assets policy")
	rest := doc[start+len(marker):]
	end := strings.Index(rest, "</code></pre>")
	require.GreaterOrEqual(t, end, 0, "the policy JSON block must be closed")

	var docPolicy map[string]any
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(rest[:end])), &docPolicy))
	require.NotEmpty(t, docPolicy, "the published policy must not be empty")

	raw := buildRemoteJobPayload(GenerateRequest{}, nil)
	require.NotEmpty(t, raw)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &payload))
	var remote map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload["remote_render"], &remote))
	policyRaw, ok := remote["editing_assets_policy"]
	require.True(t, ok, "the remote payload must publish the editing-assets policy")

	var payloadPolicy map[string]any
	require.NoError(t, json.Unmarshal(policyRaw, &payloadPolicy))

	require.Equal(t, payloadPolicy, docPolicy,
		"the document policy block and the remote payload policy must be the same document (build parity)")
}

func TestModelScriptOutputForDocumentProjectsLatestRenderedLink(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
		}},
		LocalizedRenders: []LocalizedRenderResult{
			{ClipID: "source-1", DriveLink: "https://drive.google.com/render-old"},
			{ClipID: "source-1", DriveLink: "https://drive.google.com/render-new"},
		},
	}

	model := modelScriptOutputForDocument(result, "en")
	require.Equal(t, "source-1", model.SpecScene.Scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive.google.com/render-new", model.SpecScene.Scenes[0].Bindings.Clip.DriveLink)
	// The source result remains the source-oriented payload; only the document
	// projection is late-bound to the certified output.
	require.Equal(t, "https://drive.google.com/source", result.Scenes[0].Clip.DriveLink)
}

// TestModelScriptOutputForDocumentProjectsRenderedLinkPerLanguage pins the
// language-scoped render projection. One clip is rendered once per language,
// and every language's document is rendered from the SAME run result — so a
// projection keyed on the clip alone resolved to whichever language finished
// last and every document showed the same, usually foreign, video.
func TestModelScriptOutputForDocumentProjectsRenderedLinkPerLanguage(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
		}},
		LocalizedRenders: []LocalizedRenderResult{
			{ClipID: "source-1", Language: "fr", DriveLink: "https://drive.google.com/render-fr"},
			{ClipID: "source-1", Language: "es", DriveLink: "https://drive.google.com/render-es"},
		},
	}

	fr := modelScriptOutputForDocument(result, "fr")
	es := modelScriptOutputForDocument(result, "es")
	require.Equal(t, "source-1", fr.SpecScene.Scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "https://drive.google.com/render-fr", fr.SpecScene.Scenes[0].Bindings.Clip.DriveLink)
	require.Equal(t, "https://drive.google.com/render-es", es.SpecScene.Scenes[0].Bindings.Clip.DriveLink)
	require.NotEqual(t, fr.SpecScene.Scenes[0].Bindings.Clip.DriveLink, es.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
		"two languages must never publish the same render link when each has its own certified render")
}

// TestModelScriptOutputForDocumentNeverSubstitutesAnotherLanguagesRender pins
// the fail-closed half of the projection: a render of a different language is
// not a candidate, because a document built for language X must not link
// language Y's video merely because that video exists.
func TestModelScriptOutputForDocumentNeverSubstitutesAnotherLanguagesRender(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
		}},
		LocalizedRenders: []LocalizedRenderResult{
			// The clip's French render is the ONLY render that exists (the Spanish
			// fan-out produced nothing). It must not leak into the Italian
			// document, whose document must fall back to the source clip link.
			{ClipID: "source-1", Language: "fr", DriveLink: "https://drive.google.com/render-fr"},
		},
	}

	fr := modelScriptOutputForDocument(result, "fr")
	it := modelScriptOutputForDocument(result, "it")
	require.Equal(t, "https://drive.google.com/render-fr", fr.SpecScene.Scenes[0].Bindings.Clip.DriveLink)
	require.Equal(t, "https://drive.google.com/source", it.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
		"a language with no render of its own must keep the source clip link, never a foreign language's render")
}

// TestModelScriptOutputForDocumentKeepsLanguageLessRenderAsFallback pins the
// backward-compatibility path: rows written before the per-language contract
// (no language) stay usable, but never in preference to a render that states
// its own language.
func TestModelScriptOutputForDocumentKeepsLanguageLessRenderAsFallback(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:   "scene-1",
			Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
		}},
		LocalizedRenders: []LocalizedRenderResult{
			{ClipID: "source-1", DriveLink: "https://drive.google.com/render-legacy"},
		},
	}
	it := modelScriptOutputForDocument(result, "it")
	require.Equal(t, "https://drive.google.com/render-legacy", it.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
		"a pre-contract render must still project into its document")

	// A language-specific render for the same clip always outranks the legacy
	// row, whatever the order in which the fan-out recorded them.
	result.LocalizedRenders = append([]LocalizedRenderResult{
		{ClipID: "source-1", Language: "it", DriveLink: "https://drive.google.com/render-it"},
	}, result.LocalizedRenders...)
	it = modelScriptOutputForDocument(result, "it")
	require.Equal(t, "https://drive.google.com/render-it", it.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
		"a language-specific render must outrank a language-less fallback")
}

// TestApplyLocalizedRenderLinkLockedProjectsOnlyTheSourceLanguageRender pins
// that the shared, language-less scene clip reference is deterministic: it
// carries the SOURCE-language render and never a translated variant, so the
// last language to finish can no longer overwrite it for every language.
func TestApplyLocalizedRenderLinkLockedProjectsOnlyTheSourceLanguageRender(t *testing.T) {
	newResult := func() *GenerateResult {
		return &GenerateResult{
			SourceLanguage: "en",
			Scenes: []Scene{{
				ID:   "scene-1",
				Clip: &ClipReference{ID: "source-1", DriveLink: "https://drive.google.com/source"},
			}},
		}
	}
	source := LocalizedRenderResult{ClipID: "source-1", Language: "en", DriveLink: "https://drive.google.com/render-en"}
	translated := LocalizedRenderResult{ClipID: "source-1", Language: "es", DriveLink: "https://drive.google.com/render-es"}

	// Order-independent: a translated variant never wins, whichever finishes last.
	for _, order := range [][]LocalizedRenderResult{{translated, source}, {source, translated}} {
		result := newResult()
		for _, rendered := range order {
			applyLocalizedRenderLinkLocked(result, rendered)
		}
		require.Equal(t, "https://drive.google.com/render-en", result.Scenes[0].Clip.DriveLink,
			"the shared source clip reference must carry the source-language render")
	}

	// A translated variant alone leaves the source clip identity untouched.
	translatedOnly := newResult()
	applyLocalizedRenderLinkLocked(translatedOnly, translated)
	require.Equal(t, "https://drive.google.com/source", translatedOnly.Scenes[0].Clip.DriveLink,
		"a translated variant must never overwrite the source clip's own link")

	// A run with no declared source language (restored legacy checkpoint) keeps
	// the accept-any-render behaviour instead of silently dropping the render.
	legacy := newResult()
	legacy.SourceLanguage = ""
	applyLocalizedRenderLinkLocked(legacy, translated)
	require.Equal(t, "https://drive.google.com/render-es", legacy.Scenes[0].Clip.DriveLink)
}

func TestModelScriptOutputForDocumentProjectsSegmentAnnotationsWhenScenePointerIsMissing(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{"en": "Ada Lovelace pioneered computing."},
		}},
		Segments: []scriptpkg.VidRushSegmentResult{{
			SceneID: "scene-0", Position: 0,
			Insights: scriptpkg.SegmentInsights{
				Entities:         []scriptpkg.ExtractedEntity{{Value: "Ada Lovelace", Type: "PERSON", Confidence: 1}},
				ImportantPhrases: []string{"Ada Lovelace"},
			},
		}},
	}

	model := modelScriptOutputForDocument(result, "en")
	require.NotNil(t, model.SpecScene.Scenes[0].Annotations)
	require.Len(t, model.SpecScene.Scenes[0].Annotations.PrimaryEntities, 1)
	require.Equal(t, "Ada Lovelace", model.SpecScene.Scenes[0].Annotations.PrimaryEntities[0].CanonicalName)
	require.Len(t, model.SpecScene.Scenes[0].Annotations.ImportantPhrases, 1)
}

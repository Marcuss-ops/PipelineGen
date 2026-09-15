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

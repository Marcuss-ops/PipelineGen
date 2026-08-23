// Package adapters_test — processor_clip_bindings_doc_json_diff_test.go.
//
// BEHAVIORAL BYTE-LEVEL DIFF TEST: pins the document's embedded SpecScene
// JSON snapshot to the canonical JSON-wire representation. The two surfaces
// a downstream consumer sees — the `<h2>SpecScene JSON</h2>` block inside the
// rendered Google Doc and the marshaled SpecScene response body — must be
// identical, not merely "contains the same links".
//
// The document renderer is read-only with respect to SpecScene: it marshals
// model.SpecScene verbatim. This test renders the real HTML body and
// marshals the real wire shape, then re-parses BOTH byte streams back into
// scriptpkg.SpecSceneOutput and asserts structural equality. A future
// refactor that re-introduces divergence between the two surfaces (e.g. a
// renderer that mutates or re-binds SpecScene before embedding it) fails
// here instead of silently drifting.
package adapters_test

import (
	"context"
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestSpecScene_DocumentEmbeddedJSON_Equals_JSONWire is the load-bearing
// SSOT differential: the SpecScene JSON embedded in the rendered document
// must equal the SpecScene JSON served on the wire.
func TestSpecScene_DocumentEmbeddedJSON_Equals_JSONWire(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		// PR 6 canonical: URLs keyed by the canonical (Drive file
		// ID) the user typed, NOT by any internal asset.ID.
		AcceptedClipIDs: []string{"drive-file-A", "drive-file-B", "drive-file-C", "drive-file-D"},
		ClipCount:       4,
		ClipNames: map[string]string{
			"drive-file-A": "Clip A",
			"drive-file-B": "Clip B",
			"drive-file-C": "Clip C",
			"drive-file-D": "Clip D",
		},
		DriveLinks: map[string]string{
			"drive-file-A": "https://drive.google.com/file/d/drive-file-A/view",
			"drive-file-B": "https://drive.google.com/file/d/drive-file-B/view",
			"drive-file-C": "https://drive.google.com/file/d/drive-file-C/view",
			"drive-file-D": "https://drive.google.com/file/d/drive-file-D/view",
		},
	}

	// 4 scenes, exactly one per canonical ID — no cycling. Pure
	// canonical-alignment test surface.
	const numScenes = 4
	scenes := make([]scriptpkg.SpecScene, numScenes)
	for i := range scenes {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "s" + string(rune('1'+i)),
			Index: i,
			Kind:  scriptpkg.SceneClip,
		}
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     numScenes,
	}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: scenes},
	}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// SpecScene.Version is set to 1 (canonical schema version per
	// internal/kernel/script/model_output.go) so the renderer's
	// MarshalIndent emits "version": 1, not the int zero value.
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}

	// Document surface: the canonical renderer embeds a byte-faithful
	// json.MarshalIndent snapshot of model.SpecScene.
	docHTML, err := scriptgen.RenderDocument(model, scriptgen.DocumentRenderOptions{Title: "PR 7 Test Title"})
	require.NoError(t, err)
	docRaw := extractSpecSceneJSON(t, docHTML)

	// Wire surface: the response writer marshals the same SpecScene.
	wireRaw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	require.NoError(t, err)

	var fromDoc scriptpkg.SpecSceneOutput
	var fromWire scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(docRaw), &fromDoc))
	require.NoError(t, json.Unmarshal(wireRaw, &fromWire))

	// THE LOAD-BEARING SSOT ASSERTION: the embedded SpecScene JSON and
	// the JSON-wire response must be structurally identical.
	require.Equal(t, fromWire, fromDoc,
		"embedded SpecScene JSON diverged from the wire JSON response")
}

// extractSpecSceneJSON isolates the embedded SpecScene JSON snapshot from a
// rendered document body and unescapes it for byte-faithful comparison.
func extractSpecSceneJSON(t *testing.T, output string) string {
	t.Helper()

	const startMarker = "<h2>SpecScene JSON</h2><pre><code>"
	start := strings.Index(output, startMarker)
	require.NotEqual(t, -1, start, "SpecScene JSON marker missing")
	start += len(startMarker)

	end := strings.Index(output[start:], "</code></pre>")
	require.NotEqual(t, -1, end, "SpecScene JSON closing marker missing")

	return html.UnescapeString(output[start : start+end])
}

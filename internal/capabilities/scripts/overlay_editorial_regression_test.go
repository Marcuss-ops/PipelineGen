package scriptgeneration

// Runtime regression certification (R17/R18): Rust owns the editorial and
// chapter projection. The Go producer must carry the certified manifest and
// leave the scene candidates untouched; corrupt manifest data must remain a
// no-op for the candidates sent to Rust.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func editorialRegressionResult(t *testing.T) *GenerateResult {
	t.Helper()
	return phraseCeilingFixture(t)
}

func editorialRegressionRequest(t *testing.T, result *GenerateResult) overlayplan.V2Request {
	t.Helper()
	input, err := prepareOverlayPlanCompile(result, "en", GoldenOverlayCanvas,
		"plan-fixed", "video-fixed", "project-fixed", nil)
	require.NoError(t, err)
	require.NotNil(t, input)
	request, err := buildOverlayPlanV2Request(input.result, input.language, input.canvas,
		input.planID, input.videoID, input.projectID, input.motionSeed,
		input.timedScenes, input.scenes, input.rawScenes, input.imagePlan, input.timelineBounds)
	require.NoError(t, err)
	return request
}

func marshalScenes(t *testing.T, result *GenerateResult) string {
	t.Helper()
	request := editorialRegressionRequest(t, result)
	for index := range request.Scenes {
		// Chart certification is a run-level authorization flag, not a scene
		// candidate. R18 compares candidate surfaces only.
		if request.Scenes[index].Raw != nil {
			request.Scenes[index].Raw.ChartsCertified = false
		}
	}
	raw, err := json.Marshal(request.Scenes)
	require.NoError(t, err)
	return string(raw)
}

func marshalRequest(t *testing.T, result *GenerateResult) string {
	t.Helper()
	request := editorialRegressionRequest(t, result)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	return string(raw)
}

func corruptEditorial() *scriptpkg.EditorialManifest {
	title := "Garbage Injected Title"
	return &scriptpkg.EditorialManifest{
		SchemaVersion:  scriptpkg.EditorialManifestVersion,
		SourceLanguage: "en",
		ProfileVersion: "highlights.v1",
		Scenes: []scriptpkg.EditorialScene{{
			SceneID:         "scene-0",
			Title:           &title,
			TitleStatus:     "resolved",
			TitleSource:     "topic_validated",
			Bullets:         []scriptpkg.EditorialSceneBullet{{SentenceStart: 99, SentenceEnd: 100, Text: "not in transcript"}},
			Highlights:      []scriptpkg.EditorialHighlight{{SentenceIndex: 99, StartByte: 9999, EndByte: 10000, Text: "nope", Score: 9.9, VisualEligible: true}},
			GloballyIndexed: true,
		}},
		Certified:   true,
		Fingerprint: "corrupt",
	}
}

// TestOverlayPlanLeavesEditorialProjectionToRust_R17 certifies that a valid
// manifest travels to Rust as data and is not pre-selected into the scene
// candidates by Go.
func TestOverlayPlanLeavesEditorialProjectionToRust_R17(t *testing.T) {
	plain := marshalRequest(t, editorialRegressionResult(t))

	withEditorial := editorialRegressionResult(t)
	withEditorial.SourceLanguage = "en"
	withEditorial.Output.Text = withEditorial.Scenes[0].Text["en"]
	title := "alpha one bravo"
	withEditorial.Editorial = &scriptpkg.EditorialManifest{
		SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: "en", ProfileVersion: "highlights.v1",
		Scenes:    []scriptpkg.EditorialScene{{SceneID: "scene-0", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated", Bullets: []scriptpkg.EditorialSceneBullet{}, Highlights: []scriptpkg.EditorialHighlight{}}},
		Certified: true, Fingerprint: strings.Repeat("a", 64),
	}
	compiled := marshalRequest(t, withEditorial)

	require.NotEqual(t, plain, compiled, "R17: the certified manifest must travel in the request")
	require.Contains(t, compiled, `"title":"alpha one bravo"`)
	require.NotContains(t, compiled, `"presentation_role":"scene_title"`,
		"R17: Go must not pre-select editorial titles; Rust owns the projection")
}

// TestOverlayPlanIgnoresCorruptEditorial_R18 certifies that invalid manifest
// contents do not change the scene candidates sent to Rust.
func TestOverlayPlanIgnoresCorruptEditorial_R18(t *testing.T) {
	plain := marshalScenes(t, editorialRegressionResult(t))

	withCorrupt := editorialRegressionResult(t)
	withCorrupt.Editorial = corruptEditorial()
	require.Equal(t, plain, marshalScenes(t, withCorrupt),
		"R18: corrupt editorial must not change the overlay candidates")
	require.NotNil(t, withCorrupt.Editorial.Scenes[0].Title,
		"R18: the manifest still carries candidates as data")
}

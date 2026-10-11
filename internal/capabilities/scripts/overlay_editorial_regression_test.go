package scriptgeneration

// Runtime regression certification (R17/R18): certified, grounded editorial
// data may add only its admitted title/bullet primitives; corrupt data must
// remain a no-op and never affect the rendering plan.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func editorialRegressionResult(t *testing.T) *GenerateResult {
	t.Helper()
	return phraseCeilingFixture(t)
}

func marshalPlan(t *testing.T, result *GenerateResult) string {
	t.Helper()
	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-fixed", "video-fixed", "project-fixed")
	require.NoError(t, err)
	require.NotNil(t, plan)
	raw, err := json.Marshal(plan)
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

// TestOverlayPlanProjectsCertifiedEditorial_R17 certifies that a valid
// manifest contributes its grounded title to the compiled plan.
func TestOverlayPlanProjectsCertifiedEditorial_R17(t *testing.T) {
	plain := marshalPlan(t, editorialRegressionResult(t))

	withEditorial := editorialRegressionResult(t)
	withEditorial.SourceLanguage = "en"
	withEditorial.Output.Text = withEditorial.Scenes[0].Text["en"]
	title := "alpha one bravo"
	withEditorial.Editorial = &scriptpkg.EditorialManifest{
		SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: "en", ProfileVersion: "highlights.v1",
		Scenes:    []scriptpkg.EditorialScene{{SceneID: "scene-0", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated", Bullets: []scriptpkg.EditorialSceneBullet{}, Highlights: []scriptpkg.EditorialHighlight{}}},
		Certified: true, Fingerprint: strings.Repeat("a", 64),
	}
	compiled := marshalPlan(t, withEditorial)
	require.NotEqual(t, plain, compiled, "R17: a certified grounded title is projected")
	require.Contains(t, compiled, `"editorial_role":"scene_title"`)
	require.Contains(t, compiled, `"text":"alpha one bravo"`)
}

// TestOverlayPlanIgnoresCorruptEditorial_R18 certifies that invalid manifest
// contents fail closed and leave the semantic render plan unchanged.
func TestOverlayPlanIgnoresCorruptEditorial_R18(t *testing.T) {
	plain := marshalPlan(t, editorialRegressionResult(t))

	withCorrupt := editorialRegressionResult(t)
	withCorrupt.Editorial = corruptEditorial()
	require.Equal(t, plain, marshalPlan(t, withCorrupt),
		"R18: corrupt editorial must not change the overlay plan")
	require.NotNil(t, withCorrupt.Editorial.Scenes[0].Title,
		"R18: the manifest still carries candidates as data")
}

package scriptgeneration

// Runtime regression certification (R17/R18): Fase 1 must not alter
// rendering. The overlay compiler never reads SceneHighlights/Editorial, so
// attaching them — even corrupt ones — must leave the compiled plan
// byte-identical. If a future phase wires them in, THESE TESTS MUST FAIL
// loudly instead of silently changing videos.

import (
	"encoding/json"
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

// TestOverlayPlanIgnoresEditorialManifest_R17 certifies that attaching a
// valid EditorialManifest leaves the compiled OverlayPlan byte-identical.
func TestOverlayPlanIgnoresEditorialManifest_R17(t *testing.T) {
	plain := marshalPlan(t, editorialRegressionResult(t))

	withEditorial := editorialRegressionResult(t)
	title := "Alpha One"
	withEditorial.SceneHighlights = []scriptpkg.SceneHighlight{{
		SceneID: "scene-0", Title: &title, TitleStatus: "resolved",
		TitleSource:     "topic_validated",
		Bullets:         []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "alpha one"}},
		Highlights:      []scriptpkg.ScenePhraseSpan{{SentenceIndex: 0, StartByte: 0, EndByte: 9, Text: "alpha one", Score: 0.9, VisualEligible: true}},
		GloballyIndexed: true,
	}}
	withEditorial.Editorial = &scriptpkg.EditorialManifest{
		SchemaVersion: "editorial.v1", SourceLanguage: "en", ProfileVersion: "highlights.v1",
		Certified: true, Fingerprint: "abc",
	}
	require.Equal(t, plain, marshalPlan(t, withEditorial),
		"R17: valid editorial must not change the overlay plan")
}

// TestOverlayPlanIgnoresCorruptEditorial_R18 certifies that even an invalid
// manifest cannot leak into rendering: candidates exist as data (R18) but
// are never rendered prematurely.
func TestOverlayPlanIgnoresCorruptEditorial_R18(t *testing.T) {
	plain := marshalPlan(t, editorialRegressionResult(t))

	withCorrupt := editorialRegressionResult(t)
	withCorrupt.Editorial = corruptEditorial()
	require.Equal(t, plain, marshalPlan(t, withCorrupt),
		"R18: corrupt editorial must not change the overlay plan either")
	require.NotNil(t, withCorrupt.Editorial.Scenes[0].Title,
		"R18: the manifest still carries candidates as data")
}

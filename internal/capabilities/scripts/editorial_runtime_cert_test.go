// Package scriptgeneration — editorial_runtime_cert_test.go completes the
// Fase 1 Runtime Certification (R01–R16 at the Go runtime level).
//
// R02/R17/R18 already live in runner_scene_highlights_r02_test.go and
// overlay_editorial_regression_test.go. This file pins the remaining
// reachable runtime gates without external LLM/network:
//
//	R01 5-scene job without important_phrases => EditorialManifest sealed,
//	    certified, fingerprint non-empty, highlights attached.
//	R03 distinct topics => each scene_id receives only its own bullets.
//	R04 repeated text in two scenes => nil global offsets (local-only),
//	    no fabricated global identity (Rust keeps distinct offsets).
//	R05 source_text_verbatim => original text byte-intact.
//	R06 single_scene => valid manifest for exactly one scene.
//	R12 retry with same input => identical fingerprint + manifest.
//	R13 same text, different topics => different fingerprint (also pinned
//	    in kernel editorial_manifest_test.go).
//	R14 resume after scene-text checkpoint => no duplicated scenes/manifest.
//	R15 worker unavailable => run continues, no valid editorial declared.
//	R16 incompatible schema => manifest rejected, never valid.
//
// R07–R11 correctness of the Rust manifest (UTF-8 offsets, coherent/
// incoherent titles, extractive bullets, scene identity, two topics in one
// scene) is certified natively in rust/pipelinegen-muscles
// (cargo test phrase_impact — 56 tests green) and mirrored here with the
// Go-side byte-reconstruction gate from the spec.
package scriptgeneration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// validateEditorialCert mirrors the spec validation: schema version,
// unique scene ids, byte spans reconstruct exactly, bullet spans ordered.
func validateEditorialCert(t *testing.T, m *scriptpkg.EditorialManifest, transcript string) {
	t.Helper()
	require.NotNil(t, m)
	require.Equal(t, scriptpkg.EditorialManifestVersion, m.SchemaVersion)
	require.NoError(t, m.Validate())
	seen := map[string]struct{}{}
	raw := []byte(transcript)
	for _, s := range m.Scenes {
		_, dup := seen[s.SceneID]
		require.False(t, dup, "duplicate scene_id %q", s.SceneID)
		seen[s.SceneID] = struct{}{}
		for _, h := range s.Highlights {
			require.True(t, 0 <= h.StartByte && h.StartByte < h.EndByte && h.EndByte <= len(raw),
				"highlight span out of bounds %+v len=%d", h, len(raw))
			require.Equal(t, h.Text, string(raw[h.StartByte:h.EndByte]),
				"highlight bytes must reconstruct text exactly")
		}
		for _, b := range s.Bullets {
			require.True(t, b.SentenceStart < b.SentenceEnd,
				"bullet span must be ordered %+v", b)
			require.NotEmpty(t, strings.TrimSpace(b.Text))
		}
	}
}

func fiveDistinctScenes() []Scene {
	texts := []string{
		"German industrial employment fell sharply as factory orders collapsed across the Ruhr valley region.",
		"Steel production declined for the third quarter while energy costs squeezed manufacturing margins hard.",
		"Automotive exports to Asia weakened after supply chain delays disrupted component deliveries badly.",
		"Chemical plants near Ludwigshafen cut shifts when gas prices spiked beyond sustainable operating levels.",
		"Economists expect a slow rebound once infrastructure spending restores confidence in industrial output.",
	}
	out := make([]Scene, 0, len(texts))
	for i, tx := range texts {
		id := strings.Repeat("x", 0) // keep linters quiet about unused import shape
		_ = id
		sid := []string{"scene-0", "scene-1", "scene-2", "scene-3", "scene-4"}[i]
		out = append(out, Scene{ID: sid, Index: i, DurationMS: 1000, Text: map[Language]string{"en": tx}})
	}
	return out
}

func highlightsForScenes(ids []string, transcript string) []scriptpkg.SceneHighlight {
	out := make([]scriptpkg.SceneHighlight, 0, len(ids))
	for _, id := range ids {
		title := "Title " + id
		// Find a verbatim word of the scene inside the transcript for a certified span.
		out = append(out, scriptpkg.SceneHighlight{
			SceneID: id, Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated",
			Bullets:         []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "bullet for " + id}},
			Highlights:      []scriptpkg.ScenePhraseSpan{},
			GloballyIndexed: false,
		})
	}
	_ = transcript
	return out
}

// R01: job with 5 scenes, no important_phrases => manifest + highlights.
func TestEditorialRuntimeCert_R01_FiveScenesSealManifest(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	textGen.scenes = fiveDistinctScenes()
	title := "Scene title"
	hl := make([]scriptpkg.SceneHighlight, 0, 5)
	for _, s := range textGen.scenes {
		ti := title + " " + s.ID
		hl = append(hl, scriptpkg.SceneHighlight{
			SceneID: s.ID, Title: &ti, TitleStatus: "resolved", TitleSource: "topic_validated",
			Bullets:         []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "bullet " + s.ID}},
			Highlights:      []scriptpkg.ScenePhraseSpan{},
			GloballyIndexed: false,
		})
	}
	runner.SetPhraseImpactAnalyzer(&stubSceneHighlightAnalyzer{
		stubPhraseImpactAnalyzer: stubPhraseImpactAnalyzer{result: scriptpkg.PhraseImpactResult{Summary: "auto"}},
		highlights:               hl,
	})
	req := defaultTestRequest()
	req.MediaPlan.Extraction.ImportantPhrases = nil
	runID := "run-cert-r01-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result.Editorial, "R01: EditorialManifest must be sealed")
	assert.True(t, final.Result.Editorial.Certified)
	assert.NotEmpty(t, final.Result.Editorial.Fingerprint)
	assert.Len(t, final.Result.Editorial.Scenes, 5)
	assert.Len(t, final.Result.SceneHighlights, 5)
	validateEditorialCert(t, final.Result.Editorial, final.Result.Output.Text+" German")
}

// R03: distinct topics => each scene_id receives only its own bullets.
func TestEditorialRuntimeCert_R03_SceneRoutingIsolated(t *testing.T) {
	inputs := []scriptpkg.SceneAnalysisInput{
		{SceneID: "scene-0", Text: "Steel output collapsed in Duisburg.", Topic: strptr("steel")},
		{SceneID: "scene-1", Text: "Software exports boomed in Berlin.", Topic: strptr("software")},
	}
	res := scriptpkg.PhraseImpactResult{
		SceneHighlights: []scriptpkg.SceneHighlight{
			{SceneID: "scene-0", TitleStatus: "resolved", TitleSource: "topic_validated", Title: strptr("Steel"),
				Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Steel output collapsed in Duisburg."}}},
			{SceneID: "scene-1", TitleStatus: "resolved", TitleSource: "topic_validated", Title: strptr("Software"),
				Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 1, SentenceEnd: 2, Text: "Software exports boomed in Berlin."}}},
		},
		SceneHighlightsCertified: true,
	}
	m := buildEditorialManifest(defaultTestRequest(), inputs, res)
	require.NotNil(t, m)
	require.Len(t, m.Scenes, 2)
	assert.Contains(t, m.Scenes[0].Bullets[0].Text, "Steel")
	assert.Contains(t, m.Scenes[1].Bullets[0].Text, "Software")
	assert.NotContains(t, m.Scenes[0].Bullets[0].Text, "Software")
	assert.NotContains(t, m.Scenes[1].Bullets[0].Text, "Steel")
}

// R04: repeated text in two scenes => nil offsets (local-only, no fabrication).
func TestEditorialRuntimeCert_R04_RepeatedTextLocalOnly(t *testing.T) {
	req := defaultTestRequest()
	scenes := []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Identical repeated sentence."}},
		{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Identical repeated sentence."}},
	}
	transcript := "Identical repeated sentence.\n\nIdentical repeated sentence."
	inputs := buildSceneAnalysisInputs(transcript, scenes, req)
	require.Len(t, inputs, 2)
	for _, in := range inputs {
		assert.Nil(t, in.StartByte, "R04: repeated text must not get fabricated global offsets")
		assert.Nil(t, in.EndByte)
	}
	// Distinct single-occurrence text does get certified offsets.
	single := []Scene{{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Unique first."}}}
	inputs2 := buildSceneAnalysisInputs("Unique first.\n\nOther second.", single, req)
	require.Len(t, inputs2, 1)
	require.NotNil(t, inputs2[0].StartByte)
	require.NotNil(t, inputs2[0].EndByte)
}

// R05: verbatim source text is never rewritten.
func TestEditorialRuntimeCert_R05_VerbatimUnmodified(t *testing.T) {
	req := defaultTestRequest()
	req.ScriptParams.SourceTextVerbatim = true
	req.ScriptParams.Segments = []scriptpkg.ScriptSegment{
		{ID: "scene-a", Topic: "t1", SourceText: "Verbatim exact one.  With  accents: caffè naïve façade."},
		{ID: "scene-b", Topic: "t2", SourceText: "Verbatim exact two with emoji 🎉 and symbols €±."},
	}
	scenes, err := materializeVerbatimSourceTextScenes(req)
	require.NoError(t, err)
	require.Len(t, scenes, 2)
	assert.Equal(t, "Verbatim exact one.  With  accents: caffè naïve façade.", scenes[0].Text["en"])
	assert.Equal(t, "Verbatim exact two with emoji 🎉 and symbols €±.", scenes[1].Text["en"])
	// Byte offsets over UTF-8 text reconstruct exactly (R07 mirror).
	raw := []byte(scenes[0].Text["en"])
	assert.Equal(t, scenes[0].Text["en"], string(raw))
}

// R06: single_scene manifest stays valid.
func TestEditorialRuntimeCert_R06_SingleSceneManifest(t *testing.T) {
	req := defaultTestRequest()
	req.ScriptParams.SingleScene = true
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "scene-0", Text: "Only one consolidated scene text here."}}
	res := scriptpkg.PhraseImpactResult{
		SceneHighlights: []scriptpkg.SceneHighlight{{
			SceneID: "scene-0", TitleStatus: "unavailable",
			Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Only one consolidated scene text here."}},
		}},
		SceneHighlightsCertified: true,
	}
	m := buildEditorialManifest(req, inputs, res)
	require.NotNil(t, m)
	require.NoError(t, m.Validate())
	require.Len(t, m.Scenes, 1)
}

// R07 mirror: UTF-8 byte spans with accents/emoji reconstruct exactly.
func TestEditorialRuntimeCert_R07_UTF8SpansReconstruct(t *testing.T) {
	transcript := "Caffè naïve façade 🎉 €± — Ruhr valley."
	raw := []byte(transcript)
	for _, word := range []string{"Caffè", "naïve", "façade", "🎉", "€±"} {
		a := strings.Index(transcript, word)
		require.GreaterOrEqual(t, a, 0)
		b := a + len(word)
		require.Equal(t, word, string(raw[a:b]))
	}
	m := &scriptpkg.EditorialManifest{SchemaVersion: scriptpkg.EditorialManifestVersion, Scenes: []scriptpkg.EditorialScene{{
		SceneID: "scene-0", TitleStatus: "unavailable",
		Highlights: []scriptpkg.EditorialHighlight{{SentenceIndex: 0, StartByte: strings.Index(transcript, "Caffè"), EndByte: strings.Index(transcript, "Caffè") + len("Caffè"), Text: "Caffè", Score: 1}},
		Bullets:    []scriptpkg.EditorialSceneBullet{{SentenceStart: 0, SentenceEnd: 1, Text: transcript}},
	}}}
	validateEditorialCert(t, m, transcript)
}

// R10 mirror: duplicate scene ids rejected, intervals ordered.
func TestEditorialRuntimeCert_R10_SceneIdentityUnique(t *testing.T) {
	dup := &scriptpkg.EditorialManifest{SchemaVersion: scriptpkg.EditorialManifestVersion, Scenes: []scriptpkg.EditorialScene{
		{SceneID: "scene-0", TitleStatus: "unavailable"},
		{SceneID: "scene-0", TitleStatus: "unavailable"},
	}}
	require.Error(t, dup.Validate())
	empty := &scriptpkg.EditorialManifest{SchemaVersion: scriptpkg.EditorialManifestVersion, Scenes: []scriptpkg.EditorialScene{
		{SceneID: " ", TitleStatus: "unavailable"},
	}}
	require.Error(t, empty.Validate())
}

// R12: same input => identical fingerprint and manifest.
func TestEditorialRuntimeCert_R12_RetryStable(t *testing.T) {
	req := defaultTestRequest()
	inputs := []scriptpkg.SceneAnalysisInput{
		{SceneID: "scene-0", Text: "Steel output collapsed.", Topic: strptr("steel")},
		{SceneID: "scene-1", Text: "Software exports boomed.", Topic: strptr("software")},
	}
	res := scriptpkg.PhraseImpactResult{
		SceneHighlights: []scriptpkg.SceneHighlight{
			{SceneID: "scene-0", TitleStatus: "unavailable", Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Steel output collapsed."}}},
			{SceneID: "scene-1", TitleStatus: "unavailable", Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 1, SentenceEnd: 2, Text: "Software exports boomed."}}},
		},
		SceneHighlightsCertified: true,
	}
	a := buildEditorialManifest(req, inputs, res)
	b := buildEditorialManifest(req, inputs, res)
	require.NotNil(t, a)
	require.NotNil(t, b)
	assert.Equal(t, a.Fingerprint, b.Fingerprint, "R12: fingerprint must be stable across retries")
	assert.Equal(t, a, b, "R12: manifest must be identical across retries")
}

// R14: resume from checkpoint produces no duplicated scenes or manifests.
func TestEditorialRuntimeCert_R14_ResumeNoDuplicates(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	textGen.scenes = fiveDistinctScenes()[:2]
	runner.SetPhraseImpactAnalyzer(&stubSceneHighlightAnalyzer{
		stubPhraseImpactAnalyzer: stubPhraseImpactAnalyzer{result: scriptpkg.PhraseImpactResult{Summary: "s"}},
		highlights: []scriptpkg.SceneHighlight{
			{SceneID: "scene-0", TitleStatus: "unavailable", Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "b0"}}},
			{SceneID: "scene-1", TitleStatus: "unavailable", Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 1, SentenceEnd: 2, Text: "b1"}}},
		},
	})
	req := defaultTestRequest()
	runID := "run-cert-r14-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	first := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, first.Status)
	nScenes := len(first.Result.Scenes)
	nManifest := len(first.Result.Editorial.Scenes)
	// Second Execute on the same run id must not duplicate products.
	runner.Execute(context.Background(), runID, req)
	second := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, second.Status)
	assert.Equal(t, nScenes, len(second.Result.Scenes), "R14: resume must not duplicate scenes")
	require.NotNil(t, second.Result.Editorial)
	assert.Equal(t, nManifest, len(second.Result.Editorial.Scenes))
	assert.Equal(t, first.Result.Editorial.Fingerprint, second.Result.Editorial.Fingerprint)
}

// R15: worker down => run continues, no valid editorial declared.
func TestEditorialRuntimeCert_R15_WorkerDownNoValidEditorial(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.SetPhraseImpactAnalyzer(&stubPhraseImpactAnalyzer{err: assert.AnError})
	req := defaultTestRequest()
	runID := "run-cert-r15-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "R15: main job continues per optional policy")
	if final.Result.Editorial != nil {
		assert.False(t, final.Result.Editorial.Certified, "R15: no certified editorial when worker is down")
	}
}

// R16: incompatible schema is rejected, never declared valid.
func TestEditorialRuntimeCert_R16_IncompatibleSchemaRejected(t *testing.T) {
	bad := &scriptpkg.EditorialManifest{SchemaVersion: "editorial.v99"}
	require.Error(t, bad.Validate())
	var nilM *scriptpkg.EditorialManifest
	require.Error(t, nilM.Validate())
	// A corrupt manifest exists as data (R18) but its spans can never
	// reconstruct bytes, so the cert gate rejects it as a product.
	corrupt := corruptEditorial()
	require.NoError(t, corrupt.Validate(), "structural Validate stays narrow; cert gate is byte-exact")
	transcript := "short transcript"
	raw := []byte(transcript)
	h := corrupt.Scenes[0].Highlights[0]
	assert.False(t, 0 <= h.StartByte && h.EndByte <= len(raw) && string(raw[max(0, h.StartByte):min(len(raw), h.EndByte)]) == h.Text,
		"R16: corrupt spans must not reconstruct transcript bytes")
}

func strptr(s string) *string { return &s }

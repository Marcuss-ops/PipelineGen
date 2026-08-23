// Package scriptgeneration — runner_overlay_document_ssot_test.go certifies
// Test 19's single-source-of-truth (SSOT) contract:
//
//	           OverlayPlan (result.OverlayPlan)
//	              /                \
//	             /                  \
//	Google Doc (SpecScene JSON)   RenderingGen (EnqueueChrononPlan)
//
// The document must surface the SAME overlay content — script text,
// important phrases, keywords, images and their timing — that the OverlayPlan
// carries, because both derive from the same sealed scene annotations. The
// test runs a full 3-scene COMBINED_TIMELINE job, derives the OverlayPlan
// (the RenderingGen input), renders the real Google Doc body, and proves the
// document's embedded SpecScene JSON carries byte-for-byte the same
// annotations the OverlayPlan was compiled from, that every overlay item is
// traceable to that surface, and that the human document shows the scene
// script and the overlay timing sections.
package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// overlayScene2Annotations returns the semantic surface of the third scene,
// "Elon Musk unveiled the new Tesla prototype." (7 words × 100ms):
//
//	Elon(0) Musk(1) unveiled(2) the(3) new(4) Tesla(5) prototype(6)
//	→ "new Tesla prototype" 0.4–0.7s, "Tesla" 0.5–0.6s,
//	  Elon Musk 0.0–0.2s (PERSON + image), Tesla 0.5–0.6s (LOGO + image).
func overlayScene2Annotations() *scriptpkg.SceneAnnotations {
	return &scriptpkg.SceneAnnotations{
		Version:  1,
		Language: "en",
		Status:   "completed",
		ImportantPhrases: []scriptpkg.AnnotationSpan{
			{Text: "new Tesla prototype", Score: 0.85},
		},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Tesla", Score: 1.0},
		},
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{
				ID: "entity-elon-musk", CanonicalName: "Elon Musk", Type: "PERSON", Confidence: 0.98,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "elon-musk-photo", PreviewURL: "https://cdn.example.com/elon-musk.jpg"},
			},
			{
				ID: "entity-tesla", CanonicalName: "Tesla", Type: "LOGO", Confidence: 0.96,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tesla-logo", PreviewURL: "https://cdn.example.com/tesla-logo.png"},
			},
		},
	}
}

// TestOverlayPlan_IsSingleSourceOfTruthForDocumentAndRender is the Test 19
// load-bearing assertion: after a real 3-scene run, the Google Doc and the
// OverlayPlan destined for RenderingGen share one source of truth.
func TestOverlayPlan_IsSingleSourceOfTruthForDocumentAndRender(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
		{
			ID: "scene-1", Index: 1,
			Text:        map[Language]string{"en": "Growth matters more than ever."},
			Annotations: overlayScene1Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
		{
			ID: "scene-2", Index: 2,
			Text:        map[Language]string{"en": "Elon Musk unveiled the new Tesla prototype."},
			Annotations: overlayScene2Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-ssot"

	runID := "run-overlay-ssot-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res)
	require.NotNil(t, res.OverlayPlan, "OverlayPlan (the RenderingGen input) must be derived")
	require.NoError(t, res.OverlayPlan.Validate())
	require.NotEmpty(t, res.OverlayPlan.Items, "OverlayPlan must carry overlay items for the 3 scenes")

	// ── The document published by the runner is the Google Doc body. ──────
	require.Len(t, docPub.records, 1, "exactly one document (en) published")
	docHTML := docPub.records[0].Content
	require.NotEmpty(t, docHTML)

	// ── SSOT: the document's embedded SpecScene JSON carries the exact
	//    annotations the OverlayPlan was derived from. No second logic. ─────
	docSpec := extractSpecSceneContract(t, docHTML)
	require.Len(t, docSpec.Scenes, 3, "document must describe all 3 scenes")
	for i := range docSpec.Scenes {
		require.Equal(t, res.Scenes[i].Annotations, docSpec.Scenes[i].Annotations,
			"scene %q: document annotations diverged from the OverlayPlan source", res.Scenes[i].ID)

		// Structural equality above catches semantic drift. This second
		// assertion pins the stronger wire contract: the annotation JSON
		// embedded in the document is byte-identical to the annotation JSON
		// from which the OverlayPlan was derived.
		planSourceBytes, err := json.Marshal(res.Scenes[i].Annotations)
		require.NoError(t, err, "scene %q: marshal OverlayPlan source annotations", res.Scenes[i].ID)
		documentBytes, err := json.Marshal(docSpec.Scenes[i].Annotations)
		require.NoError(t, err, "scene %q: marshal document annotations", res.Scenes[i].ID)
		require.True(t, bytes.Equal(planSourceBytes, documentBytes),
			"scene %q: document annotation bytes differ from OverlayPlan source", res.Scenes[i].ID)

		// Gate 1 (document live, byte-level): the document's embedded scene
		// body text must be byte-identical to the text NLP/TTS/overlay
		// consumed — after the single contract normalization (TrimSpace).
		// A "contains" or word-count check is not enough: any drift here
		// means the pipeline worked on a text variant the user never saw.
		require.Equal(t, strings.TrimSpace(res.Scenes[i].Text["en"]), docSpec.Scenes[i].Text,
			"scene %q: document body text diverged from the text NLP/TTS/overlay consumed", res.Scenes[i].ID)
	}

	// ── Document completeness: script text + overlay timing surfaces. ─────
	for _, scene := range res.Scenes {
		require.Contains(t, docHTML, scene.Text["en"], "document missing scene %q script text", scene.ID)
	}
	require.Contains(t, docHTML, "<h3>Phrase Timing</h3>", "document missing the overlay phrase-timing section")
	require.Contains(t, docHTML, "<strong>Start:</strong>", "document missing the overlay scene-timing section")

	// ── Traceability: every OverlayPlan item comes from the document's
	//    annotation surface (never from an independent derivation). ─────────
	texts := documentAnnotationTexts(docSpec)
	imageAssets := documentImageAssets(docSpec)
	for _, item := range res.OverlayPlan.Items {
		switch item.TemplateID {
		case "IMPORTANT_PHRASE", "IMPORTANT_WORD", "NUMBER", "QUOTE",
			"person_default", "org_default", "gpe_default", "concept_default":
			require.True(t, texts[normalizeDocText(item.Text)],
				"overlay item %q (text %q) is not present in the document", item.ID, item.Text)
		case "IMAGE_OVERLAY", "PRODUCT", "LOGO":
			require.Len(t, item.AssetRefs, 1, "image overlay %q must carry one asset", item.ID)
			require.True(t, imageAssets[item.AssetRefs[0].AssetID],
				"overlay image %q (asset %q) is not present in the document", item.ID, item.AssetRefs[0].AssetID)
		}
	}
}

// documentAnnotationTexts collects every textual overlay surface the document
// carries: important phrases, important words and entity canonical names.
func documentAnnotationTexts(spec scriptpkg.SpecSceneOutput) map[string]bool {
	out := map[string]bool{}
	for i := range spec.Scenes {
		ann := spec.Scenes[i].Annotations
		if ann == nil {
			continue
		}
		for _, span := range ann.ImportantPhrases {
			out[normalizeDocText(span.Text)] = true
		}
		for _, span := range ann.ImportantWords {
			out[normalizeDocText(span.Text)] = true
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity{}, ann.PrimaryEntities...), ann.SecondaryEntities...) {
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				name = strings.TrimSpace(entity.Text)
			}
			if name != "" {
				out[normalizeDocText(name)] = true
			}
		}
	}
	return out
}

// documentImageAssets collects every image asset id the document's entity
// image bindings reference.
func documentImageAssets(spec scriptpkg.SpecSceneOutput) map[string]bool {
	out := map[string]bool{}
	for i := range spec.Scenes {
		ann := spec.Scenes[i].Annotations
		if ann == nil {
			continue
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity{}, ann.PrimaryEntities...), ann.SecondaryEntities...) {
			if entity.Image != nil && strings.TrimSpace(entity.Image.AssetID) != "" {
				out[strings.TrimSpace(entity.Image.AssetID)] = true
			}
		}
	}
	return out
}

func normalizeDocText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

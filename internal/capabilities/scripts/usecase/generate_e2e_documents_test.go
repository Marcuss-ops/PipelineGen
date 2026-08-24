// Package scripts — generate_e2e_documents_test.go is the end-to-end
// acceptance check for the document publication surface. It exercises the
// full GenerateOneUseCase.Execute path with the real DocumentsProcessor and a
// stub Drive publisher, then asserts that the published document's human
// section shows only the title, per-scene headings/text, and the
// language-correct voiceover URL — never clip/stock/entity/description/tags,
// which must live only inside the embedded SpecScene JSON snapshot.
package usecase

import (
	"context"
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// documentsE2EStub is a capture-only DocumentsService for the document E2E
// path. It never touches Drive and records every published title + content.
type documentsE2EStub struct {
	titles  []string
	content []string
}

func (s *documentsE2EStub) CreateDoc(_ context.Context, title, content string, _ scriptports.FolderResolver, _, _ string, _ bool) (string, string, error) {
	s.titles = append(s.titles, title)
	s.content = append(s.content, content)
	return "https://docs.google.com/document/d/doc-e2e/edit", "doc-e2e", nil
}

func (s *documentsE2EStub) UpdateDoc(context.Context, string, string, string) error { return nil }

// buildUsecaseWithDocuments wires the canonical text source, the real
// clip-bindings processor, and the real documents processor (backed by the
// capture stub) into a GenerateOneUseCase.
func buildUsecaseWithDocuments(gen *fakeOllamaGen, docs scriptports.DocumentsService) *GenerateOneUseCase {
	reg := adapters.NewSourceRegistry(zap.NewNop())
	reg.Register(scriptpkg.SourceText, NewTextSourceResolver())
	reg.Freeze()

	e := buildTestEngine(gen, nil)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	ppReg.Register(adapters.NewClipBindingsProcessor(zap.NewNop()))
	ppReg.Register(adapters.NewDocumentsProcessor(docs))
	ppReg.Register(&stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	})
	ppReg.Freeze()

	return NewGenerateOneUseCase(adapters.NormalizationConfig{}, reg, e, ppReg, zap.NewNop())
}

// documentHumanSurface returns the human-facing part of a rendered document
// body (everything before the SpecScene JSON snapshot).
func documentHumanSurface(t *testing.T, output string) string {
	t.Helper()

	const marker = "<h2>SpecScene JSON</h2>"
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatalf("SpecScene JSON marker missing from document: %s", output)
	}
	return output[:index]
}

// extractDocumentSpecSceneJSON isolates and unescapes the embedded SpecScene
// JSON snapshot from a rendered document body.
func extractDocumentSpecSceneJSON(t *testing.T, output string) string {
	t.Helper()

	const startMarker = "<h2>SpecScene JSON</h2><pre><code>"
	start := strings.Index(output, startMarker)
	require.NotEqual(t, -1, start, "SpecScene JSON start marker missing")
	start += len(startMarker)

	end := strings.Index(output[start:], "</code></pre>")
	require.NotEqual(t, -1, end, "SpecScene JSON closing marker missing")

	return html.UnescapeString(output[start : start+end])
}

// TestGenerateE2E_DocumentHumanSurfaceShowsOnlyTitleScenesVoiceover runs the
// full pipeline with multilingual docs enabled and pins the human document
// surface: title first, one-based scene headings, canonical scene text, and
// the language-correct voiceover URL — while clip bindings stay out of the
// human section and remain preserved in the embedded SpecScene JSON.
func TestGenerateE2E_DocumentHumanSurfaceShowsOnlyTitleScenesVoiceover(t *testing.T) {
	t.Parallel()

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:    "scene-0",
				Index: 0,
				Text:  "TESTO SCENA UNO",
				Kind:  scriptpkg.SceneNarration,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{ClipID: "CLIP-A", DriveLink: "CLIP-A-DRIVE"},
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status: "completed",
						Links:  map[string]string{"it": "VOICE-IT", "en": "VOICE-EN"},
					},
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  "TESTO SCENA DUE",
				Kind:  scriptpkg.SceneNarration,
			},
		},
	}

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "TESTO GLOBALE CHE NON DEVE APPARIRE",
		SpecScene:     spec,
	}
	scriptJSON, err := json.Marshal(model)
	require.NoError(t, err)

	gen := &fakeOllamaGen{result: &scriptports.GenerationResult{
		Script: string(scriptJSON), WordCount: 20, EstDuration: 6, Model: "llama3:8b",
	}}

	docs := &documentsE2EStub{}
	uc := buildUsecaseWithDocuments(gen, docs)

	item := makeTextOnlyItem("e2e-doc-human", "Source text about clean energy for the document surface.")
	item.Language = "it"
	item.Title = "TITOLO DOCUMENTO"
	item.ScriptParams.SkipQualityGate = true
	item.Docs = scriptpkg.DocumentsSpec{Enabled: true, Languages: []string{"it", "en"}, FolderID: "folder-e2e"}

	_, err = uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	require.Len(t, docs.content, 2, "expected one document per language")

	humanIT := documentHumanSurface(t, docs.content[0])
	humanEN := documentHumanSurface(t, docs.content[1])

	// IT document human surface: title + one-based scene headings + canonical
	// scene text + the IT voiceover URL, and nothing technical.
	for _, want := range []string{
		"<h1>TITOLO DOCUMENTO</h1>",
		"<h2>Scene 1</h2>",
		"TESTO SCENA UNO",
		"<h2>Scene 2</h2>",
		"TESTO SCENA DUE",
		"<strong>Voiceover:</strong>",
		"VOICE-IT",
	} {
		require.Contains(t, humanIT, want)
	}
	for _, forbidden := range []string{
		"VOICE-EN",
		"TESTO GLOBALE CHE NON DEVE APPARIRE",
		"Description",
		"Tags",
	} {
		require.NotContains(t, humanIT, forbidden)
	}
	// The current document contract exposes one clickable Drive link for
	// each scene resource. The legacy Clip alias and Clips[] must not cause
	// duplicate links, but the canonical clip link is intentionally visible.
	require.Contains(t, humanIT, "<strong>Clip:</strong>")
	require.Contains(t, humanIT, "CLIP-A-DRIVE")

	// EN document shows the EN voiceover URL and never the IT one.
	require.Contains(t, humanEN, "VOICE-EN")
	require.NotContains(t, humanEN, "VOICE-IT")

	// The embedded SpecScene JSON preserves every technical binding.
	specJSON := extractDocumentSpecSceneJSON(t, docs.content[0])
	for _, want := range []string{"CLIP-A-DRIVE", "VOICE-IT", "VOICE-EN", "scene-0", "scene-1"} {
		require.Contains(t, specJSON, want)
	}
}

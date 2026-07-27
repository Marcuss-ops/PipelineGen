package adapters_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildSpecSceneDocumentHTML_RendersVisibleScenesAndLinks(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "This prose must not be duplicated in the document.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-clip-1",
					Index: 0,
					Text:  "Canonical scene text.",
					Kind:  scriptpkg.SceneNarration,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:         "clip-1",
							ClipTitle:      "Opening exchange",
							DriveLink:      "https://drive.google.com/file/d/clip-1/view",
							SubtitleFileID: "subtitle-1",
							SubtitleLink:   "https://drive.google.com/file/d/subtitle-1/view",
						},
					},
				},
			},
		},
	}
	prov := &scriptpkg.GenerationProvenance{
		DocID:         "doc-1",
		DocLink:       "https://docs.google.com/document/d/doc-1/edit",
		SourceType:    "clips",
		RequestedMode: "clip_native",
		UsedMode:      "clip_native",
	}

	html := adapters.BuildSpecSceneDocumentHTML(model, "Canonical Script", prov)

	for _, want := range []string{
		"<h1>Canonical Script</h1>",
		"<h2>Scenes</h2>",
		"<h2>SpecScene JSON</h2><pre>",
		"scene-clip-1",
		"Canonical scene text.",
		"clip-1",
		"https://drive.google.com/file/d/clip-1/view",
		"Subtitles ASS",
		"https://drive.google.com/file/d/subtitle-1/view",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected canonical document HTML to contain %q; HTML=%s", want, html)
		}
	}

	for _, unwanted := range []string{
		"<h2>Script</h2>",
		"<h2>Entities</h2>",
		"<h2>Video Metadata</h2>",
		"<h2>Technical Provenance</h2>",
		"PIPELINEGEN-PROVENANCE",
		"doc-1",
		"This prose must not be duplicated in the document.",
	} {
		if strings.Contains(html, unwanted) {
			t.Errorf("canonical SpecScene document must not contain %q; HTML=%s", unwanted, html)
		}
	}
}

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := adapters.BuildSpecSceneDocumentHTML(nil, "ignored"); got != "" {
		t.Fatalf("expected empty output for nil model, got %q", got)
	}
}

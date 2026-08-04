package adapters

import (
	"context"
	"html"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type documentServiceStub struct {
	titles  []string
	content []string
}

type emptyDocumentReferenceStub struct{}

func (*emptyDocumentReferenceStub) CreateDoc(context.Context, string, string, FolderResolver, string, string, bool) (string, string) {
	return "", ""
}

func (*emptyDocumentReferenceStub) UpdateDoc(context.Context, string, string, string) error {
	return nil
}

func (s *documentServiceStub) CreateDoc(_ context.Context, title, content string, _ FolderResolver, folderID, key string, forceRefresh bool) (string, string) {
	s.titles = append(s.titles, title+"|"+folderID+"|"+key)
	s.content = append(s.content, content)
	if forceRefresh {
		s.titles = append(s.titles, "refresh")
	}
	return "https://docs.google.com/document/d/doc-1/edit", "doc-1"
}

func TestDocumentsProcessor_UsesFullNarrativeForSingleScene(t *testing.T) {
	stub := &documentServiceStub{}
	processor := NewDocumentsProcessor(stub)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID: "run-full", Title: "Full narrative", Language: "it",
		DocsEnabled: true, DocsLanguages: []string{"it"}, SingleScene: true,
	}
	fullText := "Questo è il testo completo generato dall'endpoint, non il preview della scena."
	_, err := processor.Process(context.Background(), plan, ProcessInput{
		Text: fullText,
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", Text: "preview breve",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.content) != 1 || !strings.Contains(stub.content[0], html.EscapeString(fullText)) {
		t.Fatalf("document must contain full generated narrative: %v", stub.content)
	}
}

func (s *documentServiceStub) UpdateDoc(context.Context, string, string, string) error { return nil }

func TestDocumentsProcessor_PublishesExplicitLanguages(t *testing.T) {
	stub := &documentServiceStub{}
	processor := NewDocumentsProcessor(stub)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:            "run-1",
		Title:         "Test",
		Language:      "it",
		DocsEnabled:   true,
		DocsLanguages: []string{"it"},
		DocsFolderID:  "folder-1",
	}

	result, err := processor.Process(context.Background(), plan, ProcessInput{
		Text: "generated text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DocID != "doc-1" || result.DocLink == "" {
		t.Fatalf("unexpected document reference: %+v", result)
	}
	if len(stub.titles) != 1 || stub.titles[0] != "Test_it|folder-1|run-1-it" {
		t.Fatalf("unexpected publisher call: %v", stub.titles)
	}
}

func TestDocumentProcessorRejectsEmptyPublisherReference(t *testing.T) {
	processor := NewDocumentsProcessor(&emptyDocumentReferenceStub{})
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-empty", Title: "Test", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}}
	_, err := processor.Process(context.Background(), plan, ProcessInput{Text: "generated text"})
	if err == nil || !strings.Contains(err.Error(), "empty reference") {
		t.Fatalf("expected empty document reference error, got %v", err)
	}
}

func TestDocumentsProcessor_RendersManualVideoMetadata(t *testing.T) {
	stub := &documentServiceStub{}
	processor := NewDocumentsProcessor(stub)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:            "run-metadata",
		Title:         "Titolo interno",
		Language:      "it",
		DocsEnabled:   true,
		DocsLanguages: []string{"it"},
		VideoMetadata: &scriptpkg.VideoMetadata{
			Title:       "Titolo YouTube",
			Description: "Descrizione <manuale>",
			Tags:        []string{"boxe", "analisi"},
		},
	}

	_, err := processor.Process(context.Background(), plan, ProcessInput{Text: "Testo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.titles) != 1 || stub.titles[0] != "Titolo YouTube_it||run-metadata-it" {
		t.Fatalf("manual metadata title must name the document, calls=%v", stub.titles)
	}
	if len(stub.content) != 1 {
		t.Fatalf("expected one document body, got %d", len(stub.content))
	}
	content := stub.content[0]
	for _, want := range []string{
		"<h1>Titolo YouTube</h1>",
		"<h2>Description</h2>",
		"Descrizione &lt;manuale&gt;",
		"<h2>Tags</h2>",
		"boxe, analisi",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("document body missing %q: %s", want, content)
		}
	}
}

func TestDocumentsProcessor_RefreshesExistingDocWhenASSLinkAppears(t *testing.T) {
	stub := &documentServiceStub{}
	processor := NewDocumentsProcessor(stub)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID: "run-2", Title: "With subtitles", DocsEnabled: true,
		DocsLanguages: []string{"en"},
	}

	_, err := processor.Process(context.Background(), plan, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{SubtitleLink: "https://drive.google.com/file/d/ass/view"},
			}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.titles) != 2 || stub.titles[1] != "refresh" {
		t.Fatalf("ASS-bearing document must force refresh, calls=%v", stub.titles)
	}
}

func TestPostProcessResult_IsEmptyFalseForDocumentReference(t *testing.T) {
	if (&PostProcessResult{DocID: "doc-1", DocLink: "https://docs.google.com/document/d/doc-1/edit"}).IsEmpty() {
		t.Fatal("document reference must count as observable postprocessor output")
	}
}

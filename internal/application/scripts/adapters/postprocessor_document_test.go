package adapters

import (
	"context"
	"html"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type documentServiceStub struct{ titles, content []string }
type emptyDocumentReferenceStub struct{}

func (*emptyDocumentReferenceStub) CreateDoc(context.Context, string, string, FolderResolver, string, string, bool) (string, string, error) {
	return "", "", nil
}
func (*emptyDocumentReferenceStub) UpdateDoc(context.Context, string, string, string) error {
	return nil
}
func (s *documentServiceStub) CreateDoc(_ context.Context, title, content string, _ FolderResolver, folderID, key string, forceRefresh bool) (string, string, error) {
	s.titles = append(s.titles, title+"|"+folderID+"|"+key)
	s.content = append(s.content, content)
	if forceRefresh {
		s.titles = append(s.titles, "refresh")
	}
	return "https://docs.google.com/document/d/doc-1/edit", "doc-1", nil
}
func (s *documentServiceStub) UpdateDoc(context.Context, string, string, string) error { return nil }

func TestDocumentsProcessor_UsesFullNarrativeForSingleScene(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-full", Title: "Full narrative", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}, SingleScene: true}
	fullText := "Questo è il testo completo generato dall'endpoint, non il preview della scena."
	if _, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{Text: fullText, SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Text: "preview breve"}}}}); err != nil {
		t.Fatal(err)
	}
	if len(stub.content) != 1 || !strings.Contains(stub.content[0], html.EscapeString(fullText)) {
		t.Fatalf("document must contain full narrative: %v", stub.content)
	}
}

func TestDocumentsProcessor_PublishesExplicitLanguages(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-1", Title: "Test", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}, DocsFolderID: "folder-1"}
	result, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{Text: "generated text", SpecScene: scriptpkg.SpecSceneOutput{Version: 1}})
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
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-empty", Title: "Test", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}}
	_, err := NewDocumentsProcessor(&emptyDocumentReferenceStub{}).Process(context.Background(), plan, ProcessInput{Text: "generated text"})
	if err == nil || !strings.Contains(err.Error(), "empty reference") {
		t.Fatalf("expected empty reference error, got %v", err)
	}
}

func TestDocumentsProcessor_DisabledPlanDoesNotRequirePublisher(t *testing.T) {
	result, err := NewDocumentsProcessor(nil).Process(context.Background(), &scriptpkg.ResolvedGenerationPlan{}, ProcessInput{})
	if err != nil || result == nil || !result.Changed {
		t.Fatalf("disabled plan result=(%+v,%v), want Changed=true", result, err)
	}
}

func TestDocumentsProcessor_RendersManualVideoMetadata(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-metadata", Title: "Titolo interno", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}, VideoMetadata: &scriptpkg.VideoMetadata{Title: "Titolo YouTube", Description: "Descrizione <manuale>", Tags: []string{"boxe", "analisi"}}}
	if _, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{Text: "Testo"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Titolo YouTube</h1>", "<h2>Description</h2>", "Descrizione &lt;manuale&gt;", "<h2>Tags</h2>", "boxe, analisi"} {
		if !strings.Contains(stub.content[0], want) {
			t.Errorf("document missing %q: %s", want, stub.content[0])
		}
	}
}

func TestDocumentsProcessor_RefreshesExistingDocWhenASSLinkAppears(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-2", Title: "With subtitles", DocsLanguages: []string{"en"}, DocsEnabled: true}
	_, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{SubtitleLink: "https://drive.google.com/file/d/ass/view"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.titles) != 2 || stub.titles[1] != "refresh" {
		t.Fatalf("ASS document must refresh: %v", stub.titles)
	}
}

func TestPostProcessResult_IsEmptyFalseForDocumentReference(t *testing.T) {
	if (&PostProcessResult{DocID: "doc-1", DocLink: "https://docs.google.com/document/d/doc-1/edit"}).IsEmpty() {
		t.Fatal("document reference must be observable")
	}
}

package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type documentServiceStub struct {
	titles []string
}

func (s *documentServiceStub) CreateDoc(_ context.Context, title, _ string, _ FolderResolver, folderID, key string, forceRefresh bool) (string, string) {
	s.titles = append(s.titles, title+"|"+folderID+"|"+key)
	if forceRefresh {
		s.titles = append(s.titles, "refresh")
	}
	return "https://docs.google.com/document/d/doc-1/edit", "doc-1"
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

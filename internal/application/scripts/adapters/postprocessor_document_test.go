package adapters

import (
	"context"
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type documentServiceStub struct{ titles, content []string }
type emptyDocumentReferenceStub struct{}

func (*emptyDocumentReferenceStub) CreateDoc(context.Context, string, string, scriptports.FolderResolver, string, string, bool) (string, string, error) {
	return "", "", nil
}
func (*emptyDocumentReferenceStub) UpdateDoc(context.Context, string, string, string) error {
	return nil
}
func (s *documentServiceStub) CreateDoc(_ context.Context, title, content string, _ scriptports.FolderResolver, folderID, key string, forceRefresh bool) (string, string, error) {
	s.titles = append(s.titles, title+"|"+folderID+"|"+key)
	s.content = append(s.content, content)
	if forceRefresh {
		s.titles = append(s.titles, "refresh")
	}
	return "https://docs.google.com/document/d/doc-1/edit", "doc-1", nil
}
func (s *documentServiceStub) UpdateDoc(context.Context, string, string, string) error { return nil }

func TestDocumentsProcessor_DoesNotRewriteSingleSceneSpecScene(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-full", Title: "Full narrative", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}, SingleScene: true}
	canonical := "CANONICAL-SCENE-TEXT"
	global := "GLOBAL-TEXT-DIFFERENT"
	if _, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{Text: global, SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Text: canonical}}}}); err != nil {
		t.Fatal(err)
	}
	if len(stub.content) != 1 {
		t.Fatalf("expected one document, got %d", len(stub.content))
	}
	// The publication result carries runtime-proof metadata for GET /full.
	// The HTML itself remains the human/technical projection; these fields are
	// deliberately outside the document body.
	content := stub.content[0]
	if !strings.Contains(content, html.EscapeString(canonical)) {
		t.Errorf("document must contain canonical scene text, got: %s", content)
	}
	if strings.Contains(content, html.EscapeString(global)) {
		t.Errorf("document publisher must not rewrite SpecScene with global narrative text, got: %s", content)
	}
}

func TestDocumentsProcessor_ReportsCanonicalRendererMetadata(t *testing.T) {
	stub := &documentServiceStub{}
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "LEGACY-SCENE-ID-SENTINEL", Index: 0, Text: "CANONICAL-TEXT-SENTINEL",
		Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "SECRET-CLIP-ID"}},
	}}}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "renderer-proof", Title: "Proof", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}}
	result, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{SpecScene: spec})
	require.NoError(t, err)
	require.Equal(t, scriptgen.CanonicalDocumentRendererID, result.DocumentRenderer)
	require.Equal(t, scriptgen.SpecSceneSHA256(spec), result.DocumentSpecSceneSHA256)
	require.Equal(t, 1, result.DocumentSceneCount)
	require.Equal(t, "it", result.DocumentLanguage)
}

// TestDocumentsProcessor_OutputMatchesCanonicalRenderer pins the migrated
// renderer parity: the processor publishes byte-identical HTML to the
// canonical capability renderer for the same model + options (it no longer
// owns any HTML formatting).
func TestDocumentsProcessor_OutputMatchesCanonicalRenderer(t *testing.T) {
	stub := &documentServiceStub{}
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", Index: 0, Text: "PARITY SCENE",
		Bindings: scriptpkg.SceneBindings{
			Clip:      &scriptpkg.ClipBinding{ClipID: "CLIP-SECRET", DriveLink: "DRIVE-SECRET"},
			Voiceover: &scriptpkg.VoiceoverBinding{Status: "completed", Links: map[string]string{"it": "VOICE-IT"}},
		},
	}}}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "parity-run", Title: "Parity", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}}
	if _, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{SpecScene: spec}); err != nil {
		t.Fatal(err)
	}
	require.Len(t, stub.content, 1)

	direct, err := scriptgen.RenderDocument(&scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, SpecScene: spec}, scriptgen.DocumentRenderOptions{
		Title: "Parity", Language: "it", DefaultLanguage: "it",
	})
	require.NoError(t, err)
	require.Equal(t, direct, stub.content[0])
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

func TestDocumentsProcessor_UsesMetadataTitleOnly(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-metadata", Title: "Titolo interno", Language: "it", DocsEnabled: true, DocsLanguages: []string{"it"}, VideoMetadata: &scriptpkg.VideoMetadata{Title: "Titolo YouTube", Description: "Descrizione <manuale>", Tags: []string{"boxe", "analisi"}}}
	if _, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{Text: "Testo"}); err != nil {
		t.Fatal(err)
	}
	if len(stub.content) != 1 {
		t.Fatalf("expected one document, got %d", len(stub.content))
	}
	content := stub.content[0]
	if !strings.Contains(content, "<h1>Titolo YouTube</h1>") {
		t.Errorf("document missing metadata title: %s", content)
	}
	for _, forbidden := range []string{"<h2>Description</h2>", "Descrizione", "<h2>Tags</h2>", "boxe", "analisi"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("document must not render %q: %s", forbidden, content)
		}
	}
}

func TestResolveDocumentTitle_MetadataTitleWins(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:         "Titolo piano",
		VideoMetadata: &scriptpkg.VideoMetadata{Title: "Titolo YouTube"},
	}
	if got := resolveDocumentTitle(plan); got != "Titolo YouTube" {
		t.Fatalf("resolveDocumentTitle() = %q, want %q", got, "Titolo YouTube")
	}
}

func TestResolveDocumentTitle_FallsBackToPlanTitle(t *testing.T) {
	tests := []struct {
		name string
		plan *scriptpkg.ResolvedGenerationPlan
		want string
	}{
		{
			name: "no metadata",
			plan: &scriptpkg.ResolvedGenerationPlan{Title: "  Titolo piano  "},
			want: "Titolo piano",
		},
		{
			name: "whitespace metadata title",
			plan: &scriptpkg.ResolvedGenerationPlan{
				Title:         "Titolo piano",
				VideoMetadata: &scriptpkg.VideoMetadata{Title: "   "},
			},
			want: "Titolo piano",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDocumentTitle(tt.plan); got != tt.want {
				t.Fatalf("resolveDocumentTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveDocumentTitle_EmptyReturnsEmpty(t *testing.T) {
	if got := resolveDocumentTitle(nil); got != "" {
		t.Fatalf("resolveDocumentTitle(nil) = %q, want empty", got)
	}
	if got := resolveDocumentTitle(&scriptpkg.ResolvedGenerationPlan{}); got != "" {
		t.Fatalf("resolveDocumentTitle(empty plan) = %q, want empty", got)
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

func TestDocumentsProcessor_RefreshesWhenVoiceoverLinkExists(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-vo", Title: "With voiceover", DocsLanguages: []string{"it"}, DocsEnabled: true}
	_, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{Bindings: scriptpkg.SceneBindings{Voiceover: &scriptpkg.VoiceoverBinding{Links: map[string]string{"it": "VOICE-IT"}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.titles) != 2 || stub.titles[1] != "refresh" {
		t.Fatalf("voiceover document must refresh: %v", stub.titles)
	}
}

func TestDocumentsProcessor_RefreshesWhenMultiClipSubtitleAppears(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "run-multiclip", Title: "With multi-clip subtitles", DocsLanguages: []string{"en"}, DocsEnabled: true}
	_, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{{SubtitleLink: "https://drive.google.com/file/d/ass-b/view"}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.titles) != 2 || stub.titles[1] != "refresh" {
		t.Fatalf("multi-clip subtitle document must refresh: %v", stub.titles)
	}
}

func TestDocumentsProcessor_BuildsLanguageSpecificContent(t *testing.T) {
	stub := &documentServiceStub{}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:            "run-multi",
		Title:         "Multi voiceover",
		Language:      "it",
		DocsEnabled:   true,
		DocsLanguages: []string{"it", "en"},
	}
	_, err := NewDocumentsProcessor(stub).Process(context.Background(), plan, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID:   "scene-0",
				Text: "Scena con voiceover multilingua.",
				Bindings: scriptpkg.SceneBindings{
					Voiceover: &scriptpkg.VoiceoverBinding{
						Links: map[string]string{
							"it": "VOICE-IT",
							"en": "VOICE-EN",
						},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	require.Len(t, stub.content, 2)

	humanIT := documentHumanSection(t, stub.content[0])
	humanEN := documentHumanSection(t, stub.content[1])

	require.Contains(t, humanIT, "VOICE-IT")
	require.NotContains(t, humanIT, "VOICE-EN")
	require.Contains(t, humanEN, "VOICE-EN")
	require.NotContains(t, humanEN, "VOICE-IT")
}

// documentHumanSection returns the human-facing part of a rendered document
// body (everything before the SpecScene JSON snapshot), so assertions can
// distinguish the human surface from the JSON block that legitimately carries
// every language's voiceover link.
func documentHumanSection(t *testing.T, output string) string {
	t.Helper()

	const marker = "<h2>SpecScene JSON</h2>"
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatal("SpecScene JSON marker missing")
	}
	return output[:index]
}

func TestPostProcessResult_IsEmptyFalseForDocumentReference(t *testing.T) {
	if (&PostProcessResult{DocID: "doc-1", DocLink: "https://docs.google.com/document/d/doc-1/edit"}).IsEmpty() {
		t.Fatal("document reference must be observable")
	}
}

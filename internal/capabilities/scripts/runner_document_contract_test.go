package scriptgeneration

import (
	"context"
	"encoding/json"
	"html"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestRunnerDocumentPhase_UsesCanonicalRendererContract(t *testing.T) {
	result := &GenerateResult{
		Title: "Actors Comedy Clips",
		Scenes: []Scene{
			{
				ID: "scene-0", Index: 0,
				Text:      map[Language]string{"it": "SCENE-IT"},
				Clip:      &ClipReference{ID: "CLIP-A", DriveLink: "DRIVE-A"},
				Voiceover: map[Language]AudioReference{"it": {URL: "VOICE-IT"}},
			},
		},
	}

	model := modelScriptOutputForDocument(result, "it")
	out, err := (canonicalTestDocumentRenderer{}).RenderDocument(model, DocumentRenderOptions{
		Title: "Actors Comedy Clips", Language: "it", DefaultLanguage: "it",
	})
	require.NoError(t, err)
	require.Contains(t, out, "<h1>Actors Comedy Clips</h1>")
	require.Contains(t, out, "<h2>Scene 1</h2>")
	require.Contains(t, out, "SCENE-IT")
	require.Contains(t, out, "VOICE-IT")
	require.Contains(t, out, "<h2>SpecScene JSON</h2>")

	human := out[:strings.Index(out, "<h2>SpecScene JSON</h2>")]
	require.NotContains(t, human, "CLIP-A")
	require.Contains(t, human, "DRIVE-A")

	start := strings.Index(out, "<h2>SpecScene JSON</h2><pre><code>")
	require.NotEqual(t, -1, start)
	start += len("<h2>SpecScene JSON</h2><pre><code>")
	end := strings.Index(out[start:], "</code></pre>")
	require.NotEqual(t, -1, end)
	var decoded scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[start:start+end])), &decoded))
	require.Equal(t, model.SpecScene, decoded)
}

func TestDocumentSurfaces_ProcessorAndRunnerAreEquivalent(t *testing.T) {
	result := &GenerateResult{
		Title: "Parity",
		Scenes: []Scene{
			{
				ID: "scene-0", Index: 0,
				Text: map[Language]string{"it": "PARITY SCENE"},
				Clip: &ClipReference{ID: "CLIP-SECRET", DriveLink: "DRIVE-SECRET"},
				Voiceover: map[Language]AudioReference{
					"it": {URL: "VOICE-IT"},
					"en": {URL: "VOICE-EN"},
				},
			},
			{
				ID: "scene-1", Index: 1,
				Text: map[Language]string{"it": "PARITY SECOND SCENE"},
				Clips: []*ClipReference{
					{ID: "CLIP-B-SECRET", DriveLink: "DRIVE-B-SECRET"},
					{ID: "CLIP-C-SECRET", DriveLink: "DRIVE-C-SECRET"},
				},
			},
		},
	}

	runnerModel := modelScriptOutputForDocument(result, "it")
	runnerHTML, err := (canonicalTestDocumentRenderer{}).RenderDocument(runnerModel, DocumentRenderOptions{
		Title: "Parity", Language: "it", DefaultLanguage: "it",
	})
	require.NoError(t, err)

	service := &parityDocumentService{}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID: "parity-run", Title: "Parity", Language: "it",
		DocsEnabled: true, DocsLanguages: []string{"it"},
	}
	_, err = documentadapters.NewDocumentsProcessor(service).Process(context.Background(), plan, documentadapters.ProcessInput{
		Text: runnerModel.Text, SpecScene: runnerModel.SpecScene,
	})
	require.NoError(t, err)
	require.Len(t, service.contents, 1)

	processorHTML := service.contents[0]
	require.Equal(t, humanDocumentContract(runnerHTML), humanDocumentContract(processorHTML))
	require.Equal(t, extractSpecSceneContract(t, runnerHTML), extractSpecSceneContract(t, processorHTML))
}

type parityDocumentService struct{ contents []string }

func (s *parityDocumentService) CreateDoc(_ context.Context, _ string, content string, _ documentadapters.FolderResolver, _ string, _ string, _ bool) (string, string, error) {
	s.contents = append(s.contents, content)
	return "https://docs.example/parity", "parity-doc", nil
}

func (*parityDocumentService) UpdateDoc(context.Context, string, string, string) error { return nil }

func humanDocumentContract(output string) string {
	if index := strings.Index(output, "<h2>SpecScene JSON</h2>"); index >= 0 {
		return output[:index]
	}
	return output
}

func extractSpecSceneContract(t *testing.T, output string) scriptpkg.SpecSceneOutput {
	t.Helper()
	const startMarker = "<h2>SpecScene JSON</h2><pre><code>"
	start := strings.Index(output, startMarker)
	require.NotEqual(t, -1, start)
	start += len(startMarker)
	end := strings.Index(output[start:], "</code></pre>")
	require.NotEqual(t, -1, end)
	var decoded scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(output[start:start+end])), &decoded))
	return decoded
}

func TestGenerationRun_RenderPayloadPrecedesDocumentAndDocumentWaitsForVoiceover(t *testing.T) {
	runner, repo, _, _, voiceover, docPub, _ := newTestRunner()
	voiceover.ref.URL = "https://drive.google.com/VOICE-EN"
	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)
	req := defaultTestRequest()
	runID := "run-document-order-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	firstStart := make(map[string]time.Time)
	for _, step := range recorder.steps {
		if _, exists := firstStart[step.Name]; !exists {
			firstStart[step.Name] = step.StartedAt
		}
	}
	require.NotEmpty(t, firstStart["VOICEOVER"])
	require.NotEmpty(t, firstStart["DOCUMENT"])
	require.NotEmpty(t, firstStart["RENDER_PLAN"])
	require.True(t, firstStart["VOICEOVER"].Before(firstStart["DOCUMENT"]))
	require.True(t, firstStart["RENDER_PLAN"].Before(firstStart["DOCUMENT"]))

	// The publisher receives the voiceover-bearing document, proving that
	// document publication occurs after the voiceover phase.
	require.Contains(t, final.Result.Documents, Language("en"))
	require.Equal(t, documentadapters.CanonicalDocumentRendererID, final.Result.DocumentRenderers[Language("en")])
	require.NotEmpty(t, final.Result.DocumentSpecSceneSHA256[Language("en")])
	require.Equal(t, len(final.Result.Scenes), final.Result.DocumentSceneCounts[Language("en")])
	require.NotEmpty(t, docPub.records)
	require.Contains(t, docPub.records[0].Content, "https://drive.google.com/VOICE-EN")
}

func TestRunnerDocumentRenderingDoesNotMutateCanonicalModel(t *testing.T) {
	model := modelScriptOutputForDocument(&GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Text: map[Language]string{"en": "READ-ONLY"},
		Clip: &ClipReference{ID: "CLIP-A", DriveLink: "DRIVE-A"},
	}}}, "en")
	before, err := json.Marshal(model.SpecScene)
	require.NoError(t, err)
	_, err = (canonicalTestDocumentRenderer{}).RenderDocument(model, DocumentRenderOptions{Language: "en"})
	require.NoError(t, err)
	after, err := json.Marshal(model.SpecScene)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestGenerationRun_StageStartOrderIsMonotonic(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	recorder := &recordingExecutionRecorder{}
	runner.SetExecutionRecorder(recorder)
	req := defaultTestRequest()
	runID := "run-stage-order-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	var starts []ExecutionStep
	seen := make(map[string]bool)
	for _, step := range recorder.steps {
		if !seen[step.Name] {
			seen[step.Name] = true
			starts = append(starts, step)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].StartedAt.Before(starts[j].StartedAt) })
	var names []string
	for _, step := range starts {
		names = append(names, step.Name)
	}
	want := []string{"NORMALIZE", "SCRIPT", "TRANSLATION", "VOICEOVER", "RENDER_PLAN", "VELOX_ENQUEUE", "DOCUMENT"}
	for i, name := range want {
		require.Contains(t, names, name)
		if i > 0 {
			require.Less(t, indexOf(names, want[i-1]), indexOf(names, name))
		}
	}
}

func indexOf(values []string, wanted string) int {
	for i, value := range values {
		if value == wanted {
			return i
		}
	}
	return -1
}

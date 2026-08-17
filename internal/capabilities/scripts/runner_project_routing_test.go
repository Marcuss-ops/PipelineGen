// Package scriptgeneration — runner_project_routing_test.go certifies the
// P0 routing-context contract on the durable runner:
//
//   - a voiceover-enabled run with an empty resolved Project fails closed
//     BEFORE the first TTS call (ErrProjectRequired), never inventing a
//     "scene" namespace;
//   - the resolved Project propagates verbatim to every per-scene
//     VoiceoverInput.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// recordingProjectGenerator records the Project field of every VoiceoverInput
// it receives so the test can assert the verbatim propagation from
// GenerateRequest.Project to every scene.
type recordingProjectGenerator struct {
	mu       sync.Mutex
	projects []string
}

func (g *recordingProjectGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.projects = append(g.projects, input.Project)
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: 1.0,
	}, nil
}

// TestRunner_VoiceoverRequiresProjectBeforeTTS pins the fail-fast gate: a
// voiceover-enabled run with an empty Project must FAIL at the voiceover
// stage with zero TTS calls — the publisher's Project requirement is moved to
// the start of the phase instead of surfacing after TTS work is spent.
func TestRunner_VoiceoverRequiresProjectBeforeTTS(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &recordingProjectGenerator{}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")

	req := defaultTestRequest()
	req.Project = "" // unresolved project → fail before any TTS call

	runID := "run-project-required"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, final.Status, "voiceover without Project must fail the run")
	require.Equal(t, StageGeneratingVoiceovers, final.FailedStage)
	require.Contains(t, final.ErrorMessage, "Project is required")
	require.Equal(t, "VOICEOVER_FAILED", final.ErrorCode)

	voiceoverGen.mu.Lock()
	defer voiceoverGen.mu.Unlock()
	require.Empty(t, voiceoverGen.projects, "no TTS call may happen before Project validation")
}

// TestRunner_ProjectPropagatesToEverySceneVoiceover pins the verbatim
// propagation: the resolved GenerateRequest.Project reaches every per-scene
// VoiceoverInput so the adapter can forward it to the per-item pipeline and,
// ultimately, VoiceoverPublishCommand.Project.
func TestRunner_ProjectPropagatesToEverySceneVoiceover(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &recordingProjectGenerator{}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Project = "request-project"
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-project-propagates"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	voiceoverGen.mu.Lock()
	defer voiceoverGen.mu.Unlock()
	require.Len(t, voiceoverGen.projects, 3, "one voiceover per scene (single language)")
	for i, p := range voiceoverGen.projects {
		require.Equal(t, "request-project", p, "scene %d voiceover must carry the resolved Project", i)
	}
}

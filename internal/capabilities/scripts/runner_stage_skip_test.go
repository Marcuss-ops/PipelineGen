// Package scriptgeneration — runner_stage_skip_test.go covers the
// stage-skipping contract: when an optional port is nil OR a
// toggle in the request is false, the corresponding stage MUST be
// skipped (not panic, not attempt the call) AND the rest of the
// pipeline MUST still complete.
//
// godlike/06 SSOT invariants asserted:
//
//   - VoiceoverGenerator nil → StageGeneratingVoiceovers is
//     skipped (no AudioReference on scenes), translation and
//     audio compile still complete.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// recordingDocumentFolderResolver is the hermetic document folder authority of
// these tests: it records every (root, job, language) it is asked about and
// returns the canonical <root>/<job>/<language> id, so a test can assert BOTH
// the facts the phase resolved the folder from and the folder it published
// into.
type recordingDocumentFolderResolver struct {
	mu    sync.Mutex
	calls []documentFolderCall
	err   error
}

type documentFolderCall struct{ root, job, language string }

func (r *recordingDocumentFolderResolver) ResolveDocumentFolder(_ context.Context, root, job, language string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, documentFolderCall{root: root, job: job, language: language})
	if r.err != nil {
		return "", r.err
	}
	return root + "/" + job + "/" + language, nil
}

func (r *recordingDocumentFolderResolver) snapshot() []documentFolderCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]documentFolderCall(nil), r.calls...)
}

// TestRunner_VoiceoverGeneratorNil_StageSkipped: when voiceoverGen
// is nil, the runner MUST NOT panic and MUST skip the
// StageGeneratingVoiceovers stage. Translation + Docs + audio
// compile still complete normally.
func TestRunner_VoiceoverGeneratorNil_StageSkipped(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()

	// No voiceover generator — should be nil-safe.
	runner := NewRunner(repo, textGen, translator, nil)
	runner.SetLogger(zap.NewNop())

	req := defaultTestRequest()
	// Audio NONE: no voiceover requested, so a nil generator is skipped
	// safely and the NONE audio branch needs no voiceover assets. A
	// CHUNKED_VOICEOVER request without a generator would fail closed at
	// audio compile (ValidateChunkedVoiceovers) by design.
	req.Audio = capabilityaudio.AudioModeNone

	runID := "run-novo-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "run should complete even without voiceover")
	assert.Equal(t, StageCompleted, final.CurrentStage)

	// Voiceover stage should be skipped — no AudioReference on scenes.
	for i, s := range final.Result.Scenes {
		assert.Empty(t, s.Voiceover, "scene %d should have no voiceover when generator is nil", i)
	}

	// Other stages should complete normally.
	assert.NotEmpty(t, final.Result.Scenes[0].Text["es"], "translation should still work")
}

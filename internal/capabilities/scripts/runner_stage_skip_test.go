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
//   - Docs disabled → StagePublishingDocuments is skipped (no
//     document upserts), the run still completes.
//   - Docs enabled → StagePublishingDocuments runs and publishes
//     the configured documents, the run still completes.
package scriptgeneration

import (
	"context"
	"errors"
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
	docPub := newStubDocumentPublisher()

	// No voiceover generator — should be nil-safe.
	runner := NewRunner(repo, textGen, translator, nil, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")

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

// TestRunner_DocsDisabled_StageSkipped: when Docs.Enabled is
// false, StagePublishingDocuments is skipped AND no document
// upserts happen — the run still completes.
func TestRunner_DocsDisabled_StageSkipped(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: false} // explicitly disabled
	req.DocsEnabled = false                    // also deprecated field

	runID := "run-nodocs-001"
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
	assert.Equal(t, RunStatusCompleted, final.Status)

	// No docs published.
	assert.Equal(t, 0, len(docPub.records), "no docs should be created when disabled")

	// The run still completes.
}

// TestRunner_DocsEnabled_PublishesDocuments: when docs are explicitly
// enabled, StagePublishingDocuments runs and publishes the configured
// document set; the run completes normally.
func TestRunner_DocsEnabled_PublishesDocuments(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-norender-001"
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
	assert.Equal(t, RunStatusCompleted, final.Status)

	// Docs published (explicitly enabled).
	assert.Equal(t, 1, len(docPub.records), "one doc should be created")
}

// TestRunner_DocsEnabled_PublishesEachLanguageIntoItsRunFolder pins the document
// DESTINATION: with the folder authority wired (the same one the clip
// destination uses), every language's document publishes into
// <documents root>/<job>/<language> — the folder that language's clips publish
// into — instead of the flat documents root where no clip of that language
// lives. The two languages must not share a folder.
func TestRunner_DocsEnabled_PublishesEachLanguageIntoItsRunFolder(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	resolver := &recordingDocumentFolderResolver{}
	runner.SetDocumentFolderResolver(resolver)

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en", "it"}}

	runID := "run-docs-per-language-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.Len(t, docPub.records, 2, "one document per language")

	// Every publication used the folder the authority resolved for THAT
	// language, and the resolution was asked with the language and the job —
	// never with an empty level.
	byLanguage := map[Language]string{}
	for _, call := range resolver.snapshot() {
		assert.NotEmpty(t, call.root, "documents root must be resolved before the language level")
		assert.NotEmpty(t, call.job, "the job level must be resolved before the language level")
		byLanguage[Language(call.language)] = call.root + "/" + call.job + "/" + call.language
	}
	require.Len(t, byLanguage, 2)
	for _, rec := range docPub.records {
		want, ok := byLanguage[rec.Language]
		require.Truef(t, ok, "document for %s published without a folder resolution", rec.Language)
		assert.Equalf(t, want, rec.FolderID,
			"document for %s published into %q, want the per-language run folder %q", rec.Language, rec.FolderID, want)
	}
	assert.NotEqual(t, docPub.records[0].FolderID, docPub.records[1].FolderID,
		"two languages published their documents into one folder")
}

// TestRunner_DocsEnabled_FailsClosedWhenTheFolderCannotBeResolved: a folder
// authority that cannot resolve the per-language run folder fails the
// publication instead of silently writing the document one level up (into the
// folder holding every run).
func TestRunner_DocsEnabled_FailsClosedWhenTheFolderCannotBeResolved(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	runner.SetDocumentFolderResolver(&recordingDocumentFolderResolver{err: errors.New("drive unavailable")})

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-docs-folder-fail-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusFailed, final.Status)
	assert.Empty(t, docPub.records, "no document may publish when its run folder is unresolvable")
}

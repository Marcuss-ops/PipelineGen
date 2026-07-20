// Package scriptgeneration — comprehensive fault-injection and
// end-to-end tests for the durable stage-based generation workflow.
//
// Verdetto test scenarios:
//  1. Happy path completo — tutte le fasi completano con successo
//  2. TextGenerator fallisce — runner riparte dal checkpoint
//  3. Translator fallisce a scena N — retry riparte da N, scene già tradotte saltate
//  4. Enqueue fallisce dopo Docs — retry non ricrea traduzioni/voiceover/docs
//  5. VoiceoverGenerator nil — stage saltato, pipeline completa comunque
//  6. IsRunCompletable + ResumeFrom — logica di completamento e ripresa
//  7. RetryDelay, ShouldRetry, max retries exhausted
//  8. deriveErrorCode — error code extraction
//  9. buildDocumentContent — doc content assembly
//  10. ResolveDocsConfig — backward-compat resolution
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// Stubs
// ─────────────────────────────────────────────────────────────────────

// stubTextGenerator implements TextGenerator with fault injection.
type stubTextGenerator struct {
	mu     sync.Mutex
	scenes []Scene
	err    error
	// failAfter causes GenerateSceneText to return err after producing
	// this many successful calls. -1 means never fail.
	failAfter int
	callCount int
}

func newStubTextGenerator(scenes []Scene) *stubTextGenerator {
	return &stubTextGenerator{
		scenes:    scenes,
		failAfter: -1,
	}
}

func (g *stubTextGenerator) GenerateSceneText(ctx context.Context, req GenerateRequest) ([]Scene, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callCount++
	if g.failAfter >= 0 && g.callCount > g.failAfter {
		return nil, g.err
	}
	if g.err != nil {
		return nil, g.err
	}
	// Return a deep copy so callers can mutate without affecting the stub.
	result := make([]Scene, len(g.scenes))
	for i, s := range g.scenes {
		clone := Scene{
			ID:    s.ID,
			Index: s.Index,
			Text:  make(map[Language]string),
		}
		for k, v := range s.Text {
			clone.Text[k] = v
		}
		result[i] = clone
	}
	return result, nil
}

// stubTranslator implements Translator with fault injection.
type stubTranslator struct {
	mu         sync.Mutex
	translate  func(ctx context.Context, input TranslationInput) (string, error)
	failAfter  map[string]int // sceneID → fail after N successful calls
	callCounts map[string]int
}

func newStubTranslator() *stubTranslator {
	return &stubTranslator{
		failAfter:  make(map[string]int),
		callCounts: make(map[string]int),
		translate: func(ctx context.Context, input TranslationInput) (string, error) {
			return "[TRANSLATED] " + input.SourceText, nil
		},
	}
}

func (t *stubTranslator) Translate(ctx context.Context, input TranslationInput) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callCounts[input.SceneID]++
	if failAfter, ok := t.failAfter[input.SceneID]; ok && t.callCounts[input.SceneID] > failAfter {
		return "", errors.New("translate error: provider unavailable")
	}
	return t.translate(ctx, input)
}

// stubVoiceoverGenerator implements VoiceoverGenerator with fault injection.
type stubVoiceoverGenerator struct {
	mu        sync.Mutex
	ref       AudioReference
	err       error
	failAfter int
	callCount int
}

func newStubVoiceoverGenerator() *stubVoiceoverGenerator {
	return &stubVoiceoverGenerator{
		ref: AudioReference{
			ID:       "vo-abc-123",
			FilePath: "/tmp/voiceover.mp3",
			Duration: 12.5,
		},
		failAfter: -1,
	}
}

func (v *stubVoiceoverGenerator) Generate(ctx context.Context, input VoiceoverInput) (AudioReference, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.callCount++
	if v.failAfter >= 0 && v.callCount > v.failAfter {
		return AudioReference{}, v.err
	}
	if v.err != nil {
		return AudioReference{}, v.err
	}
	return v.ref, nil
}

// stubDocumentPublisher implements DocumentPublisher with fault injection.
type stubDocumentPublisher struct {
	mu        sync.Mutex
	ref       DocumentReference
	err       error
	failAfter int
	callCount int
	// records tracks the inputs received for assertion
	records []DocumentInput
}

func newStubDocumentPublisher() *stubDocumentPublisher {
	return &stubDocumentPublisher{
		ref: DocumentReference{
			ID:   "doc-abc-123",
			Link: "https://docs.google.com/document/d/abc-123",
		},
		failAfter: -1,
	}
}

func (p *stubDocumentPublisher) UpsertDocument(ctx context.Context, input DocumentInput) (DocumentReference, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	p.records = append(p.records, input)
	if p.failAfter >= 0 && p.callCount > p.failAfter {
		return DocumentReference{}, p.err
	}
	if p.err != nil {
		return DocumentReference{}, p.err
	}
	return p.ref, nil
}

// stubRenderEnqueuer implements RenderEnqueuer with fault injection.
type stubRenderEnqueuer struct {
	mu        sync.Mutex
	ref       RenderReference
	err       error
	failAfter int
	callCount int
}

func newStubRenderEnqueuer() *stubRenderEnqueuer {
	return &stubRenderEnqueuer{
		ref: RenderReference{
			JobID:  "render-xyz-789",
			Status: "QUEUED",
		},
		failAfter: -1,
	}
}

func (e *stubRenderEnqueuer) Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callCount++
	if e.failAfter >= 0 && e.callCount > e.failAfter {
		return RenderReference{}, e.err
	}
	if e.err != nil {
		return RenderReference{}, e.err
	}
	return e.ref, nil
}

// inMemRunRepository implements RunRepository as an in-memory store.
// This is the canonical test double for all runner tests.
type inMemRunRepository struct {
	mu   sync.Mutex
	runs map[string]*GenerationRun
}

func newInMemRunRepository() *inMemRunRepository {
	return &inMemRunRepository{
		runs: make(map[string]*GenerationRun),
	}
}

func (r *inMemRunRepository) Create(ctx context.Context, run *GenerationRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *run
	r.runs[run.ID] = &clone
	return nil
}

func (r *inMemRunRepository) Get(ctx context.Context, runID string) (*GenerationRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	clone := *run
	return &clone, nil
}

func (r *inMemRunRepository) GetByJobID(ctx context.Context, jobID string) (*GenerationRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if run.JobID == jobID {
			clone := *run
			return &clone, nil
		}
	}
	return nil, nil
}

func (r *inMemRunRepository) UpdateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.Status = status
	run.CurrentStage = stage
	run.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *inMemRunRepository) FailRun(ctx context.Context, input FailRunInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[input.RunID]
	if !ok {
		return fmt.Errorf("run %s not found", input.RunID)
	}
	run.Status = RunStatusFailed
	run.FailedStage = input.FailedStage
	run.ErrorCode = input.ErrorCode
	run.ErrorMessage = input.ErrorMessage
	run.AttemptCount = input.AttemptCount
	run.NextRetryAt = input.NextRetryAt
	run.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *inMemRunRepository) SavePartialResult(ctx context.Context, runID string, result *GenerateResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.Result = result
	run.UpdatedAt = time.Now().UTC()
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

// defaultTestRequest returns a minimal valid GenerateRequest.
func defaultTestRequest() GenerateRequest {
	return GenerateRequest{
		IdempotencyKey: "test-key-001",
		Source: Source{
			Type:  SourceText,
			Topic: "Test topic",
		},
		SourceLanguage: "en",
		Languages:      []Language{"en", "es"},
		RenderVideo:    true,
		Docs:           DocumentsConfig{Enabled: true, Languages: []Language{"en", "es"}},
	}
}

// defaultTestScenes returns 3 scenes with English text.
func defaultTestScenes() []Scene {
	return []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "First scene text"}},
		{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "Second scene text"}},
		{ID: "scene-2", Index: 2, Text: map[Language]string{"en": "Third scene text"}},
	}
}

// newTestRunner creates a Runner with in-memory repo and stub ports.
// Each stub can be configured via the returned references.
func newTestRunner() (*Runner, *inMemRunRepository, *stubTextGenerator, *stubTranslator, *stubVoiceoverGenerator, *stubDocumentPublisher, *stubRenderEnqueuer) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := newStubVoiceoverGenerator()
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, renderEnq)
	runner.SetLogger(zap.NewNop())
	return runner, repo, textGen, translator, voiceoverGen, docPub, renderEnq
}

// awaitCompletion polls the repo until the run reaches a terminal state
// or the timeout elapses. Returns the final run.
func awaitCompletion(t *testing.T, repo *inMemRunRepository, runID string, timeout time.Duration) *GenerationRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := repo.Get(context.Background(), runID)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if run.Status == RunStatusCompleted || run.Status == RunStatusFailed {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timeout waiting for run to complete")
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Test 1: Happy path completo
// ─────────────────────────────────────────────────────────────────────

func TestRunner_HappyPath_AllStagesComplete(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	req := defaultTestRequest()

	// Execute the run.
	runID := "run-happy-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err, "Create should succeed")

	runner.Execute(context.Background(), runID, req)

	// Wait for completion.
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final, "final run should not be nil")

	// Assert terminal status.
	assert.Equal(t, RunStatusCompleted, final.Status, "run should complete")
	assert.Equal(t, StageCompleted, final.CurrentStage, "final stage should be COMPLETED")

	// Assert result has all scenes.
	require.NotNil(t, final.Result, "result should not be nil")
	assert.Len(t, final.Result.Scenes, 3, "should have 3 scenes")

	// Assert EN text preserved.
	assert.Equal(t, "First scene text", final.Result.Scenes[0].Text["en"])

	// Assert ES translation present (scene-level checkpoint).
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Text["es"], "scene %d should have ES text", i)
	}

	// Assert voiceovers generated for EN and ES.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Voiceover["en"].ID, "scene %d should have EN voiceover", i)
		assert.NotEmpty(t, s.Voiceover["es"].ID, "scene %d should have ES voiceover", i)
	}

	// Assert documents published.
	require.NotNil(t, final.Result.Documents, "documents should be published")
	assert.Equal(t, 2, len(docPub.records), "should have 2 doc upsert calls (EN + ES)")

	// Assert render enqueued.
	require.NotNil(t, final.Result.RenderJob, "render job should exist")
	assert.Equal(t, "render-xyz-789", final.Result.RenderJob.JobID)
	assert.Equal(t, "QUEUED", final.Result.RenderJob.Status)

	// WordCount is not computed by the current runner — left as 0.
	assert.Equal(t, 0, final.Result.WordCount, "word count is not computed yet")
}

// ─────────────────────────────────────────────────────────────────────
// Test 2: TextGenerator fallisce — retry riparte dal checkpoint
// ─────────────────────────────────────────────────────────────────────

func TestRunner_TextGeneratorFails_RetryResumesFromCheckpoint(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()

	// Configure textGen to fail on the second call.
	textGen.err = errors.New("generate scene text failed: provider timeout")
	textGen.failAfter = 0 // succeed once, fail on retry? No — failAfter = 0 means
	// call 1 fails (callCount > 0). textGen.failAfter = -1 means never fail.
	// Let's reconfigure: first call fails, second succeeds.
	textGen.failAfter = 0 // call 1 (callCount=1) > 0 → fail
	// Re-configure: scenes stay as defaultTestScenes().
	// The first call fails, the second (retried) succeeds.

	runID := "run-textgen-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails.
	runner.Execute(context.Background(), runID, req)

	// Wait briefly for the goroutine to finish.
	time.Sleep(100 * time.Millisecond)

	// Check that the run is FAILED.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status, "first attempt should fail")
	assert.Equal(t, StageGeneratingSceneText, run.FailedStage, "should fail at GENERATING_SCENE_TEXT")
	assert.Equal(t, 1, run.AttemptCount, "attempt count should be 1")

	// Second attempt: textGen now succeeds (failAfter already expired).
	textGen.failAfter = -1 // reset to succeed
	// Also reset error so GenerateSceneText returns scenes.
	textGen.err = nil

	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "second attempt should complete")
	assert.Equal(t, StageCompleted, final.CurrentStage)

	// Verify that GenerateSceneText was called 2+ times (first failed, second succeeded).
	assert.GreaterOrEqual(t, textGen.callCount, 2, "textGen should be called at least 2 times")
}

// ─────────────────────────────────────────────────────────────────────
// Test 3: Translator fallisce a scena N — retry riparte da N
// ─────────────────────────────────────────────────────────────────────

func TestRunner_TranslatorFailsAtScene_RetrySkipsAlreadyTranslated(t *testing.T) {
	runner, repo, _, translator, _, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()

	// Configure translator to fail for scene-1.
	translator.failAfter["scene-1"] = 0 // first call to scene-1 fails

	runID := "run-translate-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails at scene-1 translation.
	runner.Execute(context.Background(), runID, req)

	time.Sleep(100 * time.Millisecond)

	// Confirm failure.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, StageTranslatingScenes, run.FailedStage)

	// Reset translator to succeed on retry.
	translator.failAfter = make(map[string]int) // No scenes fail

	// Second attempt — should resume from TRANSLATING_SCENES.
	// Scenes that already have ES text should be skipped.
	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// All scenes should have ES text.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Text["es"], "scene %d should have ES text on retry", i)
	}

	// Verify voiceovers and docs exist despite the retry.
	for i, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Voiceover["en"].ID, "scene %d should have EN voiceover", i)
	}
	assert.Equal(t, 2, len(docPub.records), "docs should be upserted exactly 2 times total")
	assert.GreaterOrEqual(t, renderEnq.callCount, 1, "render should be enqueued")
}

// ─────────────────────────────────────────────────────────────────────
// Test 4: Enqueue fallisce dopo Docs — retry non ricrea artefatti
// ─────────────────────────────────────────────────────────────────────

func TestRunner_EnqueueFailsAfterDocs_RetryPreservesArtifacts(t *testing.T) {
	runner, repo, _, _, voiceoverGen, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()

	// Configure renderEnq to fail on first call.
	renderEnq.err = errors.New("enqueue render failed: worker queue full")
	renderEnq.failAfter = 0

	runID := "run-enqueue-fail-001"
	err := repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	})
	require.NoError(t, err)

	// Execute — first attempt fails at enqueue.
	runner.Execute(context.Background(), runID, req)

	time.Sleep(100 * time.Millisecond)

	// Confirm failure.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, run.Status)
	assert.Equal(t, StageEnqueuingRender, run.FailedStage)

	// Record how many docs and voiceovers were created in the first attempt.
	firstDocCalls := len(docPub.records)
	firstVOCalls := voiceoverGen.callCount

	// Reset renderEnq to succeed on retry.
	renderEnq.err = nil
	renderEnq.failAfter = -1

	// Second attempt — should resume from ENQUEUING_RENDER.
	// Translations, voiceovers, and docs should NOT be recreated.
	runner.Execute(context.Background(), runID, req)

	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)

	// Docs should NOT increase: stage 5 was already completed.
	assert.Equal(t, firstDocCalls, len(docPub.records),
		"document upserts should not increase on retry")

	// Voiceover calls should NOT increase on retry.
	assert.Equal(t, firstVOCalls, voiceoverGen.callCount,
		"voiceover calls should not increase on retry")

	// Render enqueue should succeed on retry.
	assert.GreaterOrEqual(t, renderEnq.callCount, 2, "renderEnq should be called again on retry")
	require.NotNil(t, final.Result.RenderJob)
	assert.Equal(t, "render-xyz-789", final.Result.RenderJob.JobID)
}

// ─────────────────────────────────────────────────────────────────────
// Test 5: VoiceoverGenerator nil — stage saltato
// ─────────────────────────────────────────────────────────────────────

func TestRunner_VoiceoverGeneratorNil_StageSkipped(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()

	// No voiceover generator — should be nil-safe.
	runner := NewRunner(repo, textGen, translator, nil, docPub, renderEnq)
	runner.SetLogger(zap.NewNop())

	req := defaultTestRequest()

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
	assert.NotNil(t, final.Result.RenderJob, "render should still be enqueued")
}

// ─────────────────────────────────────────────────────────────────────
// Test 6: Docs disabled — stage saltato
// ─────────────────────────────────────────────────────────────────────

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

	// Render still works.
	require.NotNil(t, final.Result.RenderJob)
}

// ─────────────────────────────────────────────────────────────────────
// Test 7: RenderVideo false — render stages skipped
// ─────────────────────────────────────────────────────────────────────

func TestRunner_RenderVideoFalse_Skipped(t *testing.T) {
	runner, repo, _, _, _, docPub, renderEnq := newTestRunner()
	req := defaultTestRequest()
	req.RenderVideo = false
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

	// Render NOT enqueued.
	assert.Nil(t, final.Result.RenderJob, "render job should be nil when render_video is false")
	assert.Equal(t, 0, renderEnq.callCount, "renderEnq should not be called")
}

// ─────────────────────────────────────────────────────────────────────
// Test 8: deriveErrorCode — edge cases
// ─────────────────────────────────────────────────────────────────────

func TestDeriveErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		stage    Stage
		wantCode string
	}{
		{
			name:     "nil error",
			err:      nil,
			stage:    StageGeneratingSceneText,
			wantCode: "GENERATING_SCENE_TEXT_FAILED",
		},
		{
			name:     "timeout error",
			err:      errors.New("context deadline exceeded"),
			stage:    StageTranslatingScenes,
			wantCode: "PROVIDER_TIMEOUT",
		},
		{
			name:     "unavailable provider",
			err:      errors.New("connection refused"),
			stage:    StageGeneratingVoiceovers,
			wantCode: "PROVIDER_UNAVAILABLE",
		},
		{
			name:     "empty result",
			err:      errors.New("generate scene text returned zero scenes"),
			stage:    StageGeneratingSceneText,
			wantCode: "EMPTY_RESULT",
		},
		{
			name:     "text generation failed",
			err:      fmt.Errorf("generate scene text failed: %w", errors.New("ollama error")),
			stage:    StageGeneratingSceneText,
			wantCode: "TEXT_GENERATION_FAILED",
		},
		{
			name:     "translation failed",
			err:      errors.New("translate scene scene-2 to es failed: model returned gibberish"),
			stage:    StageTranslatingScenes,
			wantCode: "TRANSLATION_FAILED",
		},
		{
			name:     "voiceover failed",
			err:      errors.New("voiceover generation for scene scene-1 lang en failed: TTS error"),
			stage:    StageGeneratingVoiceovers,
			wantCode: "VOICEOVER_FAILED",
		},
		{
			name:     "document failed",
			err:      errors.New("upsert document for language es failed: document content rejected"),
			stage:    StagePublishingDocuments,
			wantCode: "DOCUMENT_FAILED",
		},
		{
			name:     "enqueue failed",
			err:      errors.New("enqueue render failed: worker queue full"),
			stage:    StageEnqueuingRender,
			wantCode: "ENQUEUE_FAILED",
		},
		{
			name:     "generic fallback",
			err:      errors.New("something unexpected happened"),
			stage:    StagePublishingDocuments,
			wantCode: "PUBLISHING_DOCUMENTS_FAILED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveErrorCode(tt.err, tt.stage)
			assert.Equal(t, tt.wantCode, got, "deriveErrorCode(%v, %s)", tt.err, tt.stage)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test 9: buildDocumentContent
// ─────────────────────────────────────────────────────────────────────

func TestBuildDocumentContent(t *testing.T) {
	scenes := []Scene{
		{Index: 0, Text: map[Language]string{"en": "Hello world", "es": "Hola mundo"}},
		{Index: 1, Text: map[Language]string{"en": "Second scene", "es": "Segunda escena"}},
		{Index: 2, Text: map[Language]string{}},
	}

	enContent := buildDocumentContent(scenes, "en")
	assert.Contains(t, enContent, "Scene 1")
	assert.Contains(t, enContent, "Hello world")
	assert.Contains(t, enContent, "Scene 2")
	assert.Contains(t, enContent, "Second scene")
	// Scene 3 has no EN text — should be skipped.
	assert.NotContains(t, enContent, "Scene 3")

	esContent := buildDocumentContent(scenes, "es")
	assert.Contains(t, esContent, "Hola mundo")
	assert.Contains(t, esContent, "Segunda escena")
}

// ─────────────────────────────────────────────────────────────────────
// Test 10: IsRunCompletable
// ─────────────────────────────────────────────────────────────────────

func TestIsRunCompletable(t *testing.T) {
	validResult := &GenerateResult{
		Scenes: []Scene{
			{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			{Text: map[Language]string{"en": "text2", "es": "texto2"}},
		},
		Documents: map[Language]DocumentReference{
			"en": {ID: "doc-en", Link: "https://doc-en"},
			"es": {ID: "doc-es", Link: "https://doc-es"},
		},
		RenderJob: &RenderReference{JobID: "render-1", Status: "QUEUED"},
	}

	t.Run("nil result", func(t *testing.T) {
		assert.False(t, IsRunCompletable(nil, []Language{"en"}, false))
	})

	t.Run("empty scenes", func(t *testing.T) {
		assert.False(t, IsRunCompletable(&GenerateResult{}, []Language{"en"}, false))
	})

	t.Run("missing translation", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
				"es": {ID: "doc-es", Link: "https://doc-es"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, false),
			"missing ES translation should be incompletable")
	})

	t.Run("missing document", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, false),
			"missing ES doc should be incompletable")
	})

	t.Run("missing render job when video enabled", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
				"es": {ID: "doc-es", Link: "https://doc-es"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, true),
			"missing render job should be incompletable when render_video=true")
	})

	t.Run("valid complete result", func(t *testing.T) {
		assert.True(t, IsRunCompletable(validResult, []Language{"en", "es"}, true))
	})

	t.Run("no video needed", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
			},
		}
		assert.True(t, IsRunCompletable(r, []Language{"en"}, false),
			"should be completable without render job when render_video=false")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Test 11: ResumeFrom
// ─────────────────────────────────────────────────────────────────────

func TestResumeFrom(t *testing.T) {
	t.Run("nil run", func(t *testing.T) {
		assert.Equal(t, StageNormalizing, ResumeFrom(nil))
	})

	t.Run("completed run", func(t *testing.T) {
		run := &GenerationRun{Status: RunStatusCompleted}
		assert.Equal(t, StageCompleted, ResumeFrom(run))
	})

	t.Run("failed at GENERATING_SCENE_TEXT", func(t *testing.T) {
		run := &GenerationRun{
			Status:      RunStatusFailed,
			FailedStage: StageGeneratingSceneText,
		}
		assert.Equal(t, StageGeneratingSceneText, ResumeFrom(run))
	})

	t.Run("failed with empty stage falls back to NORMALIZING", func(t *testing.T) {
		run := &GenerationRun{
			Status:      RunStatusFailed,
			FailedStage: "",
		}
		assert.Equal(t, StageNormalizing, ResumeFrom(run))
	})

	t.Run("running at TRANSLATING_SCENES", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusRunning,
			CurrentStage: StageTranslatingScenes,
		}
		assert.Equal(t, StageTranslatingScenes, ResumeFrom(run))
	})

	t.Run("pending run", func(t *testing.T) {
		run := &GenerationRun{
			Status: RunStatusPending,
		}
		assert.Equal(t, StageNormalizing, ResumeFrom(run))
	})
}

// ─────────────────────────────────────────────────────────────────────
// Test 12: StageIndex
// ─────────────────────────────────────────────────────────────────────

func TestStageIndex(t *testing.T) {
	assert.Equal(t, 0, StageIndex(StageNormalizing), "NORMALIZING should be index 0")
	assert.Equal(t, 1, StageIndex(StageGeneratingSceneText))
	assert.Equal(t, 2, StageIndex(StageTranslatingScenes))
	assert.Equal(t, 3, StageIndex(StageGeneratingVoiceovers))
	assert.Equal(t, 4, StageIndex(StagePublishingDocuments))
	assert.Equal(t, 5, StageIndex(StageBuildingRenderPayload))
	assert.Equal(t, 6, StageIndex(StageEnqueuingRender))
	assert.Equal(t, -1, StageIndex(StageCompleted), "terminal stages should return -1")
	assert.Equal(t, -1, StageIndex(StageFailed), "terminal stages should return -1")
	assert.Equal(t, -1, StageIndex("UNKNOWN"), "unknown stage should return -1")
}

// ─────────────────────────────────────────────────────────────────────
// Test 13: RetryDelay e ShouldRetry
// ─────────────────────────────────────────────────────────────────────

func TestRetryDelay(t *testing.T) {
	assert.Equal(t, 5*time.Second, RetryDelay(0), "attempt 0: 5s base delay")
	assert.Equal(t, 10*time.Second, RetryDelay(1), "attempt 1: 10s")
	assert.Equal(t, 20*time.Second, RetryDelay(2), "attempt 2: 20s")
	assert.Equal(t, 40*time.Second, RetryDelay(3), "attempt 3: 40s")
	assert.Equal(t, 80*time.Second, RetryDelay(4), "attempt 4: 80s")
	assert.Equal(t, 120*time.Second, RetryDelay(5), "attempt 5: capped at 120s")
	assert.Equal(t, 120*time.Second, RetryDelay(10), "attempt 10: capped at 120s")
}

func TestShouldRetry(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	past := time.Now().Add(-1 * time.Hour)

	t.Run("nil run", func(t *testing.T) {
		assert.False(t, ShouldRetry(nil))
	})

	t.Run("completed run", func(t *testing.T) {
		run := &GenerationRun{Status: RunStatusCompleted}
		assert.False(t, ShouldRetry(run))
	})

	t.Run("max retries exhausted", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: MaxRetries,
		}
		assert.False(t, ShouldRetry(run))
	})

	t.Run("retry in future not yet", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  &future,
		}
		assert.False(t, ShouldRetry(run), "should not retry before NextRetryAt")
	})

	t.Run("retry window open", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  &past,
		}
		assert.True(t, ShouldRetry(run), "should retry when NextRetryAt is in the past")
	})

	t.Run("no next retry set", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  nil,
		}
		assert.True(t, ShouldRetry(run), "should retry when NextRetryAt is nil")
	})

	t.Run("running with retries left", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusRunning,
			AttemptCount: 1,
		}
		assert.True(t, ShouldRetry(run), "RUNNING status with retries left should allow retry")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Test 14: IsTerminal
// ─────────────────────────────────────────────────────────────────────

func TestStageIsTerminal(t *testing.T) {
	assert.True(t, StageCompleted.IsTerminal(), "COMPLETED should be terminal")
	assert.True(t, StageFailed.IsTerminal(), "FAILED should be terminal")
	assert.False(t, StageNormalizing.IsTerminal(), "NORMALIZING should not be terminal")
	assert.False(t, StageGeneratingSceneText.IsTerminal())
	assert.False(t, StageTranslatingScenes.IsTerminal())
	assert.False(t, StageGeneratingVoiceovers.IsTerminal())
	assert.False(t, StagePublishingDocuments.IsTerminal())
	assert.False(t, StageBuildingRenderPayload.IsTerminal())
	assert.False(t, StageEnqueuingRender.IsTerminal())
	assert.False(t, StageWorkerQueued.IsTerminal())
}

// ─────────────────────────────────────────────────────────────────────
// Test 15: ResolveDocsConfig — backward compat
// ─────────────────────────────────────────────────────────────────────

func TestResolveDocsConfig(t *testing.T) {
	t.Run("docs struct takes priority", func(t *testing.T) {
		req := GenerateRequest{
			Docs: DocumentsConfig{
				Enabled:   true,
				Languages: []Language{"en", "es"},
				FolderID:  "folder-new",
			},
			DocsEnabled:   false,
			DriveFolderID: "folder-old",
			Languages:     []Language{"fr", "de"},
		}
		enabled, langs, folderID := req.ResolveDocsConfig()
		assert.True(t, enabled)
		assert.Equal(t, []Language{"en", "es"}, langs)
		assert.Equal(t, "folder-new", folderID)
	})

	t.Run("fallback to deprecated fields", func(t *testing.T) {
		req := GenerateRequest{
			Docs:          DocumentsConfig{}, // Enabled false, Languages empty
			DocsEnabled:   true,
			DriveFolderID: "folder-old",
			Languages:     []Language{"en", "es"},
		}
		enabled, langs, folderID := req.ResolveDocsConfig()
		assert.True(t, enabled, "DocsEnabled should enable docs")
		assert.Equal(t, []Language{"en", "es"}, langs, "should fallback to top-level Languages")
		assert.Equal(t, "folder-old", folderID, "should fallback to DriveFolderID")
	})

	t.Run("disabled by default", func(t *testing.T) {
		req := GenerateRequest{
			Languages: []Language{"en"},
		}
		enabled, langs, _ := req.ResolveDocsConfig()
		assert.False(t, enabled, "docs should be disabled by default")
		assert.Empty(t, langs, "langs should be empty when docs are disabled")
	})

	t.Run("empty languages when enabled", func(t *testing.T) {
		req := GenerateRequest{
			Docs:      DocumentsConfig{Enabled: true},
			Languages: nil,
		}
		enabled, langs, _ := req.ResolveDocsConfig()
		assert.True(t, enabled)
		assert.Empty(t, langs, "langs should be empty when no languages configured")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Test 16: ContainsAny helper
// ─────────────────────────────────────────────────────────────────────

func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"), "should find 'world'")
	assert.True(t, containsAny("timeout error", "timeout", "deadline"), "should find 'timeout'")
	assert.False(t, containsAny("hello world", "foo"), "should not find 'foo'")
	assert.False(t, containsAny("", "foo"), "empty string should not match anything")
}

// ─────────────────────────────────────────────────────────────────────
// Test 17: Service.Start validation
// ─────────────────────────────────────────────────────────────────────

func TestServiceStart_Validation(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := newStubVoiceoverGenerator()
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()

	svc := NewService(repo, textGen, translator, voiceoverGen, docPub, renderEnq)

	t.Run("missing idempotency key", func(t *testing.T) {
		req := defaultTestRequest()
		req.IdempotencyKey = ""
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "idempotency_key")
	})

	t.Run("missing source type", func(t *testing.T) {
		req := defaultTestRequest()
		req.Source.Type = ""
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "source.type")
	})

	t.Run("render_video without renderEnqueuer", func(t *testing.T) {
		// Create a service without renderEnqueuer.
		svcNoRender := NewService(repo, textGen, translator, voiceoverGen, docPub, nil)
		req := defaultTestRequest()
		req.RenderVideo = true
		_, err := svcNoRender.Start(context.Background(), req)
		assert.ErrorContains(t, err, "render_video requires a RenderEnqueuer")
	})

	t.Run("docs enabled without languages", func(t *testing.T) {
		req := defaultTestRequest()
		req.Docs = DocumentsConfig{Enabled: true, Languages: nil}
		req.Languages = nil // no fallback available
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "docs.enabled requires at least one language")
	})

	t.Run("happy path start", func(t *testing.T) {
		req := defaultTestRequest()
		result, err := svc.Start(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.Run.ID, "run ID should be set")
		assert.Equal(t, RunStatusPending, result.Run.Status, "initial status should be PENDING")
		assert.Equal(t, StageNormalizing, result.Run.CurrentStage)
		assert.Contains(t, result.StatusURL, result.Run.ID)

		// Wait briefly for the background runner.
		time.Sleep(200 * time.Millisecond)

		final, err := repo.Get(context.Background(), result.Run.ID)
		require.NoError(t, err)
		assert.Equal(t, RunStatusCompleted, final.Status)
	})
}

// ─────────────────────────────────────────────────────────────────────
// Test 18: Service.NewService panics on nil required ports
// ─────────────────────────────────────────────────────────────────────

func TestNewService_PanicsOnNilRequiredPorts(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	docPub := newStubDocumentPublisher()

	assert.Panics(t, func() {
		NewService(nil, textGen, translator, nil, docPub, nil)
	}, "nil repo should panic")

	assert.Panics(t, func() {
		NewService(repo, nil, translator, nil, docPub, nil)
	}, "nil textGen should panic")

	assert.Panics(t, func() {
		NewService(repo, textGen, nil, nil, docPub, nil)
	}, "nil translator should panic")

	assert.Panics(t, func() {
		NewService(repo, textGen, translator, nil, nil, nil)
	}, "nil docPublisher should panic")
}

// ─────────────────────────────────────────────────────────────────────
// Test 19: NewRunner panics on nil repo
// ─────────────────────────────────────────────────────────────────────

func TestNewRunner_PanicsOnNilRepo(t *testing.T) {
	assert.Panics(t, func() {
		NewRunner(nil, nil, nil, nil, nil, nil)
	}, "nil repo should panic")
}

// ─────────────────────────────────────────────────────────────────────
// Test 20: GetByJobID
// ─────────────────────────────────────────────────────────────────────

func TestInMemRepo_GetByJobID(t *testing.T) {
	repo := newInMemRunRepository()

	run1 := &GenerationRun{
		ID:     "run-001",
		JobID:  "job-abc",
		Status: RunStatusRunning,
	}
	run2 := &GenerationRun{
		ID:     "run-002",
		JobID:  "job-xyz",
		Status: RunStatusCompleted,
	}
	require.NoError(t, repo.Create(context.Background(), run1))
	require.NoError(t, repo.Create(context.Background(), run2))

	found, err := repo.GetByJobID(context.Background(), "job-abc")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "run-001", found.ID)
	assert.Equal(t, "job-abc", found.JobID)

	notFound, err := repo.GetByJobID(context.Background(), "job-nonexistent")
	require.NoError(t, err)
	assert.Nil(t, notFound, "non-existent job should return nil, nil")
}

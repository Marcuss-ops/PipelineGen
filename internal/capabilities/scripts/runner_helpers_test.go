// Package scriptgeneration — runner_helpers_test.go is the SHARED
// TEST FIXTURE layer for the per-scenario runner_test.go split
// (Fase 5 of Largest-Files plan). All stub ports, the in-memory
// RunRepository, the default request/scenes factories, and the
// per-runner setup helpers live here so each scenario file can
// stay focused on its own assertions.
//
// Per-scenario test files:
//
//   - runner_happy_path_test.go   — TestRunner_HappyPath_AllStagesComplete
//   - runner_retry_test.go        — 3 retry-from-checkpoint scenarios
//     (TestRunner_TextGeneratorFails_RetryResumesFromCheckpoint,
//     TestRunner_TranslatorFailsAtScene_RetrySkipsAlreadyTranslated,
//     TestRunner_EnqueueFailsAfterDocs_RetryPreservesArtifacts)
//   - runner_stage_skip_test.go   — 3 stage-skip scenarios
//     (TestRunner_VoiceoverGeneratorNil_StageSkipped,
//     TestRunner_DocsDisabled_StageSkipped,
//     TestRunner_RenderVideoFalse_Skipped)
//   - runner_unit_test.go         — TestDeriveErrorCode + TestBuildDocumentContent + TestContainsAny
//   - runner_lifecycle_test.go    — IsRunCompletable + ResumeFrom + StageIndex +
//     StageIsTerminal + RetryDelay + ShouldRetry + ResolveDocsConfig
//   - runner_service_test.go      — TestServiceStart_Validation +
//     TestNewService_PanicsOnNilRequiredPorts +
//     TestNewRunner_PanicsOnNilRepo + TestInMemRepo_GetByJobID
//
// godlike/06 SSOT: every stub returns ... نفس productions surface
// ports (TextGenerator / Translator / VoiceoverGenerator /
// DocumentPublisher / RenderEnqueuer) so the runner cannot tell
// the difference between a real provider and a stub. Fault
// injection is parameterized via `err` + `failAfter` fields so
// each scenario can configure the failure surface independently
// without touching the other stubs.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ─────────────────────────────────────────────────────────────────────
// Stubs — implement the 5 production ports of Runner.
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
			ID:         s.ID,
			Index:      s.Index,
			DurationMS: s.DurationMS,
			Audio:      s.Audio,
			Clip:       s.Clip,
			Text:       make(map[Language]string),
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

// ─────────────────────────────────────────────────────────────────────
// Repository stub
// ─────────────────────────────────────────────────────────────────────

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
// Factories — used by every per-scenario test file.
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
		{ID: "scene-0", Index: 0, DurationMS: 1000, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "First scene text"}},
		{ID: "scene-1", Index: 1, DurationMS: 1000, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "Second scene text"}},
		{ID: "scene-2", Index: 2, DurationMS: 1000, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "Third scene text"}},
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

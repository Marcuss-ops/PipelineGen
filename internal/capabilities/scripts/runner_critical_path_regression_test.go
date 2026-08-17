// Package scriptgeneration — runner_critical_path_regression_test.go pins the
// two "off-critical-path" guarantees of the SceneTextReady DAG with blocking
// fakes: a slow/blocked overlay.prepare enqueue and a slow/blocked
// DocsPrepare render must NEVER hold up TTS or NLP. Each test blocks the
// branch under test and then asserts TTS and NLP have already completed while
// the branch is still blocked — so a refactor that re-serializes either branch
// onto the critical path fails here.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Blocking/counting fakes ──────────────────────────────────────────

// blockingPrepareEnqueuer blocks EnqueuePrepare until release is closed, so a
// test can hold overlay.prepare open while observing that TTS/NLP are already
// done. hasStarted records the moment the enqueue begins (before blocking).
type blockingPrepareEnqueuer struct {
	mu      sync.Mutex
	started bool
	release chan struct{}
}

func (e *blockingPrepareEnqueuer) EnqueuePrepare(ctx context.Context, _ capabilityoverlay.PrepareRequest) error {
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *blockingPrepareEnqueuer) hasStarted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

// blockingDocumentRenderer blocks RenderDocument until release is closed. It
// lets a test hold DocsPrepare open and prove TTS/NLP finished before docs
// work even began.
type blockingDocumentRenderer struct {
	mu      sync.Mutex
	started bool
	release chan struct{}
}

func (r *blockingDocumentRenderer) RenderDocument(_ *scriptpkg.ModelScriptOutputV1, _ DocumentRenderOptions) (string, error) {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	<-r.release
	return "<html></html>", nil
}

func (r *blockingDocumentRenderer) hasStarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// countingEnricher is a non-blocking SegmentEnricher that counts calls, so a
// test can observe that NLP fully completed while another branch is blocked.
type countingEnricher struct {
	mu    sync.Mutex
	calls int
}

func (e *countingEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
	}, nil
}

func (e *countingEnricher) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// wireCountingVidRush attaches a counting (non-blocking) NLP enricher so the
// run's SceneAnalysis branch completes and its completion can be observed.
func wireCountingVidRush(runner *Runner) *countingEnricher {
	enricher := &countingEnricher{}
	runner.SetVidRushPipeline(&VidRushPipeline{
		Enricher: enricher,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "test"}, nil
		}),
		Backpressure: DefaultVidRushBackpressure(),
	})
	return enricher
}

// TestOverlayPrepare_DoesNotBlockTTSOrNLP pins the prepare branch: while the
// overlay.prepare enqueue is blocked, TTS has already produced every scene
// voiceover and NLP has already finished every enrichment. The run remains
// blocked only on the prepare join — never on TTS/NLP — so prepare sits off
// the TTS/NLP critical path.
func TestOverlayPrepare_DoesNotBlockTTSOrNLP(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{{
		ID:          "scene-0",
		Index:       0,
		Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
		Annotations: overlayScene0Annotations(),
		Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
	}})
	docPub := newStubDocumentPublisher()
	voGen := newStubVoiceoverGenerator()
	prepEnq := &blockingPrepareEnqueuer{release: make(chan struct{})}

	runner := NewRunner(repo, textGen, newStubTranslator(), voGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(prepEnq)
	enricher := wireCountingVidRush(runner)

	req := defaultTestRequest()
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-prepare-no-block"

	runID := "run-overlay-prepare-no-block"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// The prepare enqueue must begin (and block)...
	require.Eventually(t, prepEnq.hasStarted, 5*time.Second, time.Millisecond,
		"overlay.prepare must start")

	// ...while TTS completes and NLP has already completed. If prepare were on
	// the critical path (enqueued synchronously before TTS/NLP), these would
	// still be zero.
	require.Eventually(t, func() bool { return voCallCount(voGen) == 1 }, 5*time.Second, time.Millisecond,
		"TTS must complete all scene voiceovers while overlay.prepare is blocked")
	require.Equal(t, 1, enricher.callCount(),
		"NLP must complete before overlay.prepare is enqueued (prepare is downstream of NLP)")

	// The run is blocked only on the prepare join — not completed, not failed.
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotEqual(t, RunStatusCompleted, run.Status,
		"run must still be blocked on the prepare join, not finished early")

	// Release prepare; the run joins and completes.
	close(prepEnq.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing overlay.prepare")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestDocsPrepare_DoesNotBlockTTSOrNLP pins the docs branch: while the
// DocsPrepare render is blocked (the last phase), TTS has already produced
// every voiceover and NLP has already finished every enrichment. Docs work
// therefore sits after the TTS/NLP/audio critical path and can never hold it
// up.
func TestDocsPrepare_DoesNotBlockTTSOrNLP(t *testing.T) {
	runner, repo, _, _, voGen, _, _ := newTestRunner()
	renderer := &blockingDocumentRenderer{release: make(chan struct{})}
	runner.SetDocumentRenderer(renderer)
	enricher := wireCountingVidRush(runner)

	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-docs-no-block"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// The DocsPrepare render must begin (and block)...
	require.Eventually(t, renderer.hasStarted, 5*time.Second, time.Millisecond,
		"DocsPrepare render must start")

	// ...only after TTS and NLP have already fully completed. If docs were on
	// the critical path before TTS/NLP, these would still be incomplete here.
	require.Equal(t, len(defaultTestScenes()), voCallCount(voGen),
		"TTS must complete all scene voiceovers before DocsPrepare renders")
	require.Equal(t, len(defaultTestScenes()), enricher.callCount(),
		"NLP must complete all scene enrichments before DocsPrepare renders")

	// Release the renderer; the run finishes docs and completes.
	close(renderer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing DocsPrepare")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

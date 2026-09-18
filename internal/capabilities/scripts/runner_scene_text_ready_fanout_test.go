// Package scriptgeneration — runner_scene_text_ready_fanout_test.go certifies
// the canonical SceneTextReady fan-out: committing scene text starts
// SceneAnalysis (VidRush) per scene, and translation + TTS run as the other
// branches of the same fan-out — so TTS completes while analysis is still
// pending, never after the VidRush barrier.
package scriptgeneration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// voCallCount reads the stub voiceover generator's call counter under its
// mutex, so the test can observe TTS progress without a data race.
func voCallCount(v *stubVoiceoverGenerator) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.callCount
}

// TestSceneTextReady_TTSDoesNotWaitForAnalysis pins the fan-out contract: with
// SceneAnalysis blocked (enricher unreleased), the runner still completes
// translation + TTS. TTS therefore starts from the SceneTextReady boundary in
// parallel with analysis instead of waiting for the VidRush barrier.
func TestSceneTextReady_TTSDoesNotWaitForAnalysis(t *testing.T) {
	runner, repo, _, _, voGen, _, _ := newTestRunner()
	timeline := &timelineRecorder{}
	enricher := newE2EBlockingEnricher(timeline)
	runner.SetVidRushPipeline(&VidRushPipeline{
		Enricher: enricher,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "test"}, nil
		}),
		Backpressure: DefaultVidRushBackpressure(),
	})

	req := defaultTestRequest()
	runID := "run-fanout-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until TTS has started. Analysis is still blocked (we have not
	// released the enricher), so a non-zero voiceover call count proves TTS
	// did not wait for the analysis branch.
	deadline := time.Now().Add(5 * time.Second)
	for voCallCount(voGen) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start while SceneAnalysis was still pending")
		}
		time.Sleep(time.Millisecond)
	}
	require.Greater(t, enricher.callCount(), 0, "SceneAnalysis must have started (and be blocked) when TTS starts")

	// Release analysis and let the run join + complete.
	close(enricher.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing SceneAnalysis")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Equal(t, 3, enricher.callCount(), "each scene enriched exactly once")

	names := timeline.names()
	// Every scene analysis began during/after generation; the barrier (join)
	// completed last — after TTS and all enrichments.
	require.Contains(t, names, "vidrush scene-0 started")
	require.Contains(t, names, "vidrush scene-0 completed")
}

// ── Translated NLP ↔ TTS overlap (audit P2) ──────────────────────────
//
// runTranslatedNLP used to run AFTER the SceneTextReady join, i.e. after TTS had
// already finished — pure serial tail latency that the live probe measured in
// the minutes. It depends only on the translations and the source annotations,
// never on TTS, so it now rides the semantic branch. These fakes hold TTS open
// and prove the translated pass still runs.

// blockingVoiceoverGenerator holds every synthesis call until release is closed,
// so a test can keep the TTS branch in flight while observing the other branch.
type blockingVoiceoverGenerator struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
}

func newBlockingVoiceoverGenerator() *blockingVoiceoverGenerator {
	return &blockingVoiceoverGenerator{started: make(chan struct{}), release: make(chan struct{})}
}

func (v *blockingVoiceoverGenerator) Generate(ctx context.Context, input VoiceoverInput) (AudioReference, error) {
	v.startOne.Do(func() { close(v.started) })
	select {
	case <-v.release:
	case <-ctx.Done():
		return AudioReference{}, ctx.Err()
	}
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: 1,
	}, nil
}

// signalingTranslatedNER closes translatedCalled the first time it is invoked
// with TRANSLATED text, so the test can distinguish the translated pass from the
// source extraction that shares the same port.
type signalingTranslatedNER struct {
	translatedCalled chan struct{}
	once             sync.Once
}

func newSignalingTranslatedNER() *signalingTranslatedNER {
	return &signalingTranslatedNER{translatedCalled: make(chan struct{})}
}

func (n *signalingTranslatedNER) Extract(_ context.Context, text string, _ int) ([]VisualEntity, error) {
	if strings.Contains(text, "[TRANSLATED]") {
		n.once.Do(func() { close(n.translatedCalled) })
	}
	return nil, nil
}

// TestSceneTextReady_TranslatedNLPOverlapsTTS pins the overlap: with TTS held
// open, the translated NER still runs. Under the previous sequencing (translated
// NLP after the join) this test times out, because the pass could not start
// until TTS had finished.
func TestSceneTextReady_TranslatedNLPOverlapsTTS(t *testing.T) {
	repo := newInMemRunRepository()
	voGen := newBlockingVoiceoverGenerator()
	runner := NewRunner(repo, newStubTextGenerator(defaultTestScenes()), newStubTranslator(), voGen,
		newStubDocumentPublisher(), canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	ner := newSignalingTranslatedNER()
	runner.SetVidRushPipeline(&VidRushPipeline{
		NERPort: ner,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "test"}, nil
		}),
		Backpressure: DefaultVidRushBackpressure(),
	})

	req := defaultTestRequest()
	req.MediaPlan.Extraction = mediadomain.MediaExtractionPolicy{
		Include: []string{mediadomain.ExtractionIncludeEntities}, MaxEntitiesPerSegment: 3,
	}
	runID := "run-nlp-overlap-tts"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	select {
	case <-voGen.started:
	case <-time.After(5 * time.Second):
		t.Fatal("TTS never started")
	}

	// TTS is blocked here. The translated pass must still complete — it does not
	// depend on TTS, so it must not be queued behind the TTS join.
	select {
	case <-ner.translatedCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("translated NLP only ran after TTS completed: it is serialized behind the TTS join")
	}

	close(voGen.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after releasing TTS")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestSceneTextPathReasonNamesTheBlockingGate pins the P6 instrument. The
// audit asked to verify whether production runs actually use per-scene
// streaming "from logs/plan" — but the runner kept the decision in a local
// variable and left no trace of it, so the question could only be inferred
// from the durable Scene.TextReadyAt family after the fact. The reason must
// name the FIRST gate that forced the batch path, in the same order the runner
// evaluates them, so a single log line answers "why did this run not stream?".
func TestSceneTextPathReasonNamesTheBlockingGate(t *testing.T) {
	streamGen := newGatedStreamingTextGenerator(defaultTestScenes())
	batchGen := &stubTextGenerator{}

	clipsRequest := func() GenerateRequest {
		req := defaultTestRequest()
		req.Source = Source{Type: SourceClips, ClipIDs: []string{"clip-1"}}
		return req
	}
	withPhraseHints := func() GenerateRequest {
		req := clipsRequest()
		req.MediaPlan.Extraction.ImportantPhrases = []string{"Mike Tyson"}
		return req
	}
	withIntro := func() GenerateRequest {
		req := clipsRequest()
		req.Intro = &scriptpkg.FixedSection{}
		return req
	}
	withVerbatim := func() GenerateRequest {
		req := clipsRequest()
		req.ScriptParams.SourceTextVerbatim = true
		return req
	}
	withSceneMarkers := func() GenerateRequest {
		req := clipsRequest()
		req.Source.SourceText = "SCENE 1: the opening beat\nSCENE 2: the closing beat"
		return req
	}

	tests := []struct {
		name     string
		req      GenerateRequest
		streamed bool
		topology bool
		gen      TextGenerator
		want     string
	}{
		{
			name: "streaming run reports the streaming path",
			req:  clipsRequest(), streamed: true, gen: streamGen,
			want: "streamed",
		},
		{
			name: "verbatim source text wins over every later gate",
			req:  withVerbatim(), gen: streamGen,
			want: "batch_source_text_verbatim",
		},
		{
			name: "explicit phrase hints must be materialized before any consumer",
			req:  withPhraseHints(), gen: streamGen,
			want: "batch_important_phrase_hints",
		},
		{
			name: "literal intro/outro are injected post-LLM",
			req:  withIntro(), gen: streamGen,
			want: "batch_intro_outro",
		},
		{
			name: "a segment budget with no explicit segments needs whole-prose topology",
			req:  clipsRequest(), topology: true, gen: streamGen,
			want: "batch_segment_topology",
		},
		{
			name: "clips carrying SCENE N: markers are not streamable",
			req:  withSceneMarkers(), gen: streamGen,
			want: "batch_source_clips_ineligible",
		},
		{
			name: "a generator without the streaming surface cannot stream",
			req:  defaultTestRequest(), gen: batchGen,
			want: "batch_generator_not_streamable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sceneTextPathReason(tc.req, tc.streamed, tc.topology, tc.gen)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestSceneStreamingEligibilityRequiresStreamableClips pins the safety gate
// itself: eligibility is a property of the request alone, and the SCENE N:
// marker check is the canonical signal because bindExplicitClipSceneText WILL
// fire on those markers and could overwrite already-emitted scene text.
func TestSceneStreamingEligibilityRequiresStreamableClips(t *testing.T) {
	clips := func() GenerateRequest {
		req := defaultTestRequest()
		req.Source = Source{Type: SourceClips, ClipIDs: []string{"clip-1"}}
		return req
	}

	require.True(t, SceneStreamingEligibility(clips()), "marker-free clip requests are streamable")

	withMarkers := clips()
	withMarkers.Source.SourceText = "SCENE 1: the opening beat"
	require.False(t, SceneStreamingEligibility(withMarkers), "SCENE N: markers force the batch path")

	noClips := clips()
	noClips.Source.ClipIDs = nil
	require.False(t, SceneStreamingEligibility(noClips), "a clip source with no clips has no per-scene stream")

	require.False(t, SceneStreamingEligibility(defaultTestRequest()), "a non-clip source is not the clip streaming path")
}

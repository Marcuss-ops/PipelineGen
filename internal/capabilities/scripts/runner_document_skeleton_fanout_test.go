// Package scriptgeneration — runner_document_skeleton_fanout_test.go pins the
// early/late document-render split: the scene-text-only skeleton is rendered
// at SceneTextReady (overlapping TTS/NLP) and the late-bound artifacts are
// injected only after the audio join, with byte-identical output to the
// one-shot renderer.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingSplittableDocumentRenderer implements both DocumentRenderer and
// SplittableDocumentRenderer and signals when the early skeleton pass runs, so
// a test can prove the skeleton is rendered at SceneTextReady (overlapping
// TTS) rather than at the document phase.
type recordingSplittableDocumentRenderer struct {
	mu               sync.Mutex
	skeletonCalls    int
	skeletonRendered chan struct{}
}

func (r *recordingSplittableDocumentRenderer) RenderDocument(model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) (string, error) {
	return RenderDocument(model, opts)
}

func (r *recordingSplittableDocumentRenderer) RenderDocumentSkeleton(in DocumentSkeletonInput) string {
	r.mu.Lock()
	r.skeletonCalls++
	r.mu.Unlock()
	select {
	case r.skeletonRendered <- struct{}{}:
	default:
	}
	return RenderDocumentSkeleton(in)
}

func (r *recordingSplittableDocumentRenderer) InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) string {
	return InjectDocumentLateBound(skeleton, model, opts)
}

func (r *recordingSplittableDocumentRenderer) skeletonCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skeletonCalls
}

// TestDocumentSkeleton_RendersAtSceneTextReadyBeforeTTSCompletes pins the
// fan-out contract: the document skeleton is rendered while TTS is still
// blocked, so the early DocsPrepare pass overlaps TTS/NLP instead of waiting
// for the audio join.
func TestDocumentSkeleton_RendersAtSceneTextReadyBeforeTTSCompletes(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{{
		ID:    "scene-0",
		Index: 0,
		Text:  map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
		Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
	}})
	docPub := newStubDocumentPublisher()
	blockingVO := &blockingTimingVoiceoverGenerator{release: make(chan struct{})}
	renderer := &recordingSplittableDocumentRenderer{skeletonRendered: make(chan struct{}, 1)}

	runner := NewRunner(repo, textGen, newStubTranslator(), blockingVO, docPub, renderer)
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-doc-skeleton-fanout"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Execute(context.Background(), runID, req)
	}()

	// Wait until TTS has started (and is blocked), then assert the skeleton
	// was already rendered while TTS was still outstanding.
	deadline := time.Now().Add(5 * time.Second)
	for blockingVO.started.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("TTS did not start")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-renderer.skeletonRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("document skeleton was not rendered while TTS was still blocked")
	}
	select {
	case <-done:
		t.Fatal("run completed while TTS was still blocked")
	default:
	}

	// Release TTS; the run completes and the late-bound injection produces the
	// final document from the early skeleton.
	close(blockingVO.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not complete after TTS released")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
}

// TestDocumentSplit_IsByteEquivalentToOneShot pins that the early skeleton +
// late-bound injection path (used by the runner's SceneTextReady fan-out) is
// byte-identical to the one-shot RenderDocument for a non-nil model.
func TestDocumentSplit_IsByteEquivalentToOneShot(t *testing.T) {
	result := &GenerateResult{
		Title: "Actors Comedy Clips",
		Scenes: []Scene{{
			ID:    "scene-0",
			Index: 0,
			Text:  map[Language]string{"en": "Tim Cook said Apple changed everything in Cupertino."},
		}},
	}
	lang := Language("en")
	model := modelScriptOutputForDocument(result, lang)
	opts := DocumentRenderOptions{Title: "Actors Comedy Clips", Language: lang, DefaultLanguage: lang}

	oneShot, err := RenderDocument(model, opts)
	require.NoError(t, err)

	skeletonInput := documentSkeletonInputForScenes("Actors Comedy Clips", result.Scenes, []Language{lang})[lang]
	split := InjectDocumentLateBound(RenderDocumentSkeleton(skeletonInput), model, opts)

	require.Equal(t, oneShot, split, "the split path must be byte-equivalent to the one-shot renderer")
}

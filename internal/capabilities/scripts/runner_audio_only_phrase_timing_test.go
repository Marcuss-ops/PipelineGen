// Package scriptgeneration — runner_audio_only_phrase_timing_test.go certifies
// the audio-only path end to end:
//
//	SCRIPT   → scenes + per-scene text
//	TIMING   → per-scene word-level SpeechTimingArtifact (same synthesis stream)
//	PHRASE   → phrase→timestamp projection derived WITHOUT video render
//	M4A      → certified final_audio.m4a + canonical timeline + audio plan
//	DOC      → published Google Doc
//	VIDEO    → none (audio-only contract)
//
// The voiceover stub returns the word boundaries captured in the SAME
// synthesis stream as the audio (the Edge contract: audio chunks and
// WordBoundary chunks come from one TTS pass, never a separate transcription).
package scriptgeneration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// timingVoiceoverGenerator returns a voiceover whose AudioReference carries
// the canonical word-level timing artifact captured in the same synthesis
// stream (100ms per whitespace-delimited word).
type timingVoiceoverGenerator struct{}

func (g *timingVoiceoverGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
	words := strings.Fields(input.Text)
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, w := range words {
		boundaries[i] = capabilityaudio.SpeechWordTiming{
			Index:   i,
			Text:    w,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: float64(len(words)) * 0.1,
		Timing: &capabilityaudio.SpeechTimingArtifact{
			Version:      capabilityaudio.SpeechTimingVersion,
			Provider:     "edge_tts",
			BoundaryMode: capabilityaudio.BoundaryWord,
			Language:     string(input.Language),
			TextSHA256:   "text-hash",
			AudioSHA256:  "audio-hash",
			DurationUS:   int64(len(words)) * 100_000,
			Words:        boundaries,
		},
	}, nil
}

// TestRunner_AudioOnly_PhraseTimingsProducedWithoutVideoRender certifies the
// full audio-only chain: script/entities/timing/phrase/m4a/doc all PASS with
// zero video jobs enqueued. Phrase timings are produced without any video
// render — the phrase projection is a pure audio/timeline derivation and
// never gates on the video path.
func TestRunner_AudioOnly_PhraseTimingsProducedWithoutVideoRender(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &timingVoiceoverGenerator{}
	docPub := newStubDocumentPublisher()
	renderEnq := newStubRenderEnqueuer()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	runID := "run-audio-only-phrase-timing"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-only run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res, "result must be present")

	// SCRIPT — scenes survive with non-empty source-language text.
	require.Len(t, res.Scenes, 3, "all text-source scenes must survive")

	// TIMING — every scene captured a valid word-level timing artifact in the
	// same synthesis stream as its audio.
	for _, scene := range res.Scenes {
		ref, ok := scene.Voiceover["en"]
		require.True(t, ok, "scene %s must carry an en voiceover", scene.ID)
		require.NotNil(t, ref.Timing, "scene %s must carry word timing", scene.ID)
		require.NoError(t, ref.Timing.Validate(), "scene %s timing must be valid", scene.ID)
	}

	// PHRASE — one projection per scene, derived WITHOUT video render, with
	// the canonical local→global mapping and the canonical timeline offset.
	require.Len(t, res.PhraseTimings, 3, "phrase timings must be produced for all scenes")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")
	require.Len(t, res.CanonicalTimeline.Segments, 3)

	// SCENE SPEECH TIMING — the scene-level projection bundles each scene's
	// word boundaries with the same phrase spans, still derived from the
	// canonical timeline offset without interpolation.
	require.Len(t, res.SceneSpeechTimings, 3, "scene speech timings must be produced for all scenes")
	for i, st := range res.SceneSpeechTimings {
		require.NoError(t, st.Validate(), "scene %d speech timing must be valid", i)
		require.Equal(t, res.Scenes[i].ID, st.SceneID)
		require.Len(t, st.Phrases, 1, "scene %d must anchor its narration as one phrase", i)
		require.Equal(t, res.PhraseTimings[i], st.Phrases[0])
	}

	for i, p := range res.PhraseTimings {
		require.NoError(t, p.Validate(), "phrase %d must satisfy the master invariant", i)
		require.Equal(t, i, p.SceneIndex)
		require.Equal(t, 0, p.PhraseIndex)
		require.Equal(t, int64(0), p.LocalStartUS, "phrase %d local start must be the first word's start", i)
		require.Equal(t, res.CanonicalTimeline.Segments[i].TimelineStartUS, p.TimelineStartUS,
			"phrase %d must use the scene's canonical timeline offset", i)
		require.Equal(t, p.TimelineStartUS+p.LocalStartUS, p.GlobalStartUS)
		require.Equal(t, p.TimelineStartUS+p.LocalEndUS, p.GlobalEndUS)
	}

	// M4A — the combined master and its plan/timeline were certified.
	require.NotNil(t, res.FinalAudio, "final_audio.m4a must be certified")
	require.NotNil(t, res.AudioPlan, "audio plan must be persisted")
	require.Equal(t, capabilityaudio.FinalAudioCopy, res.AudioStrategy)

	// DOC — the Google Doc was published with id + link.
	require.NotNil(t, res.Documents["en"], "document must be published")
	require.NotEmpty(t, res.Documents["en"].ID, "document id must be present")
	require.NotEmpty(t, res.Documents["en"].Link, "document link must be present")

	// VIDEO — the audio-only contract: no render job, zero video enqueues.
	// The phrase projection never gates on the video path.
	require.Equal(t, 0, renderEnq.callCount, "video render must never be enqueued")
}

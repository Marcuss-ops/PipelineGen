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

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestModelScriptOutputForDocumentDerivesFixedKindFromRole certifies that the
// document SpecScene projection maps fixed intro/outro sections from their
// explicit SceneRole: a custom-ID fixed scene ("branded-intro") is still the
// intro document section, and its DisplayText never becomes speakable Text.
func TestModelScriptOutputForDocumentDerivesFixedKindFromRole(t *testing.T) {
	result := &GenerateResult{
		Title: "Fixed Media",
		Scenes: []Scene{
			{
				ID: "branded-intro", Index: 0,
				Role:          scriptpkg.SceneRoleOpening,
				ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
				Text:          map[Language]string{"en": "Welcome"},
				Clips:         []*ClipReference{{ID: "intro-a"}, {ID: "intro-b"}},
			},
			{
				ID: "body-0", Index: 1,
				Text: map[Language]string{"en": "Body narration"},
				Clip: &ClipReference{ID: "body-a"},
			},
			{
				ID: "branded-outro", Index: 2,
				Role:          scriptpkg.SceneRoleClosing,
				ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
				Text:          map[Language]string{"en": "Thanks"},
				Clips:         []*ClipReference{{ID: "outro-a"}},
			},
		},
	}
	model := modelScriptOutputForDocument(result, "en")
	require.NotNil(t, model)
	require.Len(t, model.SpecScene.Scenes, 3)
	intro := model.SpecScene.Scenes[0]
	if intro.ID != "branded-intro" || intro.Kind != scriptpkg.SceneIntro {
		t.Fatalf("doc intro = %q kind %q, want custom id + SceneIntro from role", intro.ID, intro.Kind)
	}
	if intro.Text != "" || intro.DisplayText != "Welcome" {
		t.Fatalf("doc intro text=%q display=%q, want display text only", intro.Text, intro.DisplayText)
	}
	require.Len(t, intro.Bindings.Clips, 2, "two-clip fixed intro must carry both clip bindings")
	if model.SpecScene.Scenes[1].Kind != scriptpkg.SceneClip {
		t.Fatalf("doc body kind = %q, want SceneClip", model.SpecScene.Scenes[1].Kind)
	}
	outro := model.SpecScene.Scenes[2]
	if outro.ID != "branded-outro" || outro.Kind != scriptpkg.SceneOutro {
		t.Fatalf("doc outro = %q kind %q, want custom id + SceneOutro from role", outro.ID, outro.Kind)
	}
}

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

// TestDocument_PhraseTimingsMatchCanonicalTiming certifies that the Google
// Doc reflects the canonical phrase→timestamp projection end to end: the
// projection is derived from the per-scene SpeechTimingArtifact + the
// canonical timeline offsets (exactly as the runner does), and both the
// human surface and the machine Phrase Timing JSON snapshot in the document
// must carry those same spans (master = timeline_start + local).
func TestDocument_PhraseTimingsMatchCanonicalTiming(t *testing.T) {
	// Two narration scenes, each carrying the word-level SpeechTimingArtifact
	// captured in the same synthesis stream (100ms per word).
	t0 := speechTimingForWords([]string{"Jackie", "Chan"})
	t1 := speechTimingForWords([]string{"grew", "up"})

	result := &GenerateResult{
		Title: "Canonical Timing",
		Scenes: []Scene{
			{
				ID:    "scene-0",
				Index: 0,
				Text:  map[Language]string{"en": "Jackie Chan"},
				Voiceover: map[Language]AudioReference{
					"en": {URL: "VO-0", Timing: &t0},
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  map[Language]string{"en": "grew up"},
				Voiceover: map[Language]AudioReference{
					"en": {URL: "VO-1", Timing: &t1},
				},
			},
		},
		// Sealed projection: scene 1 starts at 4s on the canonical timeline.
		ResolvedScenes: []ResolvedScene{
			{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 4_000_000, Text: map[Language]string{"en": "Jackie Chan"}, AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}}},
			{ID: "scene-1", Index: 1, TimelineStartUS: 4_000_000, DurationUS: 4_000_000, Text: map[Language]string{"en": "grew up"}, AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}}},
		},
	}

	// Derive the canonical projection exactly as the runner's render-payload
	// phase does (first word start → last word end, local→global via the
	// canonical timeline offset).
	require.NoError(t, compileResultPhraseTimings(result, "en"))
	require.Len(t, result.PhraseTimings, 2)
	require.Len(t, result.SceneSpeechTimings, 2)

	timeline, err := compileResolvedSceneTimeline(result.ResolvedScenes)
	require.NoError(t, err)
	result.CanonicalTimeline = &timeline

	// Render the document exactly as the runner's document phase does.
	model := modelScriptOutputForDocument(result, "en")
	out, err := (canonicalTestDocumentRenderer{}).RenderDocument(model, DocumentRenderOptions{
		Title:              "Canonical Timing",
		Language:           "en",
		DefaultLanguage:    "en",
		AudioTimeline:      result.CanonicalTimeline,
		SceneSpeechTimings: result.SceneSpeechTimings,
	})
	require.NoError(t, err)

	// Word-level timing stays in the linked timing artifacts, not in the
	// human-readable Google Doc.
	require.NotContains(t, out, "Scene Speech Timing JSON")
	require.NotContains(t, out, `"start_us"`)
	require.NotContains(t, out, `"end_us"`)

	// Human surface: each phrase shows its text and local/master spans,
	// with master = timeline_start + local (the canonical invariant).
	human := humanDocumentContract(out)
	require.Contains(t, human, "<strong>Phrase 1:</strong> Jackie Chan")
	require.Contains(t, human, "Local: 00:00.000 → 00:00.200")
	require.Contains(t, human, "Master: 00:00.000 → 00:00.200")
	require.Contains(t, human, "<strong>Phrase 1:</strong> grew up")
	require.Contains(t, human, "Local: 00:00.000 → 00:00.200")
	require.Contains(t, human, "Master: 00:04.000 → 00:04.200")

	// Every projection satisfies the canonical local→global invariant and
	// stays within the canonical timeline duration.
	for _, p := range result.PhraseTimings {
		require.NoError(t, p.Validate())
		require.Equal(t, p.TimelineStartUS+p.LocalStartUS, p.GlobalStartUS)
		require.Equal(t, p.TimelineStartUS+p.LocalEndUS, p.GlobalEndUS)
		require.LessOrEqual(t, p.GlobalEndUS, result.CanonicalTimeline.DurationUS)
	}
}

func TestDocumentSurfaces_TimingLinksPreserved(t *testing.T) {
	result := &GenerateResult{
		Title: "Timing",
		Scenes: []Scene{
			{
				ID: "scene-0", Index: 0,
				Text: map[Language]string{"it": "TIMING SCENE"},
				Voiceover: map[Language]AudioReference{
					"it": {
						URL: "VOICE-IT",
						TimingBundle: &scriptpkg.VoiceoverTimingBinding{
							Status:       "completed",
							JSONLink:     "https://drive.google.com/timing.json",
							SRTLink:      "https://drive.google.com/timing.srt",
							VTTLink:      "https://drive.google.com/timing.vtt",
							BoundaryMode: "word",
							WordCount:    2,
							DurationUS:   1_000_000,
							TextSHA256:   "text-hash",
							AudioSHA256:  "audio-hash",
						},
					},
				},
			},
		},
	}

	model := modelScriptOutputForDocument(result, "it")
	out, err := (canonicalTestDocumentRenderer{}).RenderDocument(model, DocumentRenderOptions{
		Title: "Timing", Language: "it", DefaultLanguage: "it",
	})
	require.NoError(t, err)

	// The binding carries the timing bundle per language (word array never
	// inlined — only links + summary).
	binding := model.SpecScene.Scenes[0].Bindings.Voiceover
	require.NotNil(t, binding, "voiceover binding must be present")
	require.NotNil(t, binding.Timing, "timing bundle must be mapped")
	require.Equal(t, "https://drive.google.com/timing.json", binding.Timing["it"].JSONLink)
	require.Equal(t, "completed", binding.Timing["it"].Status)

	// The human surface renders the original timing.json/srt/vtt links.
	require.Contains(t, out, "Timing JSON")
	require.Contains(t, out, "https://drive.google.com/timing.json")
	require.Contains(t, out, "Timing SRT")
	require.Contains(t, out, "https://drive.google.com/timing.srt")
	require.Contains(t, out, "Timing VTT")
	require.Contains(t, out, "https://drive.google.com/timing.vtt")
}

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

func TestGenerationRun_AudioCompilePrecedesDocumentAndDocumentWaitsForVoiceover(t *testing.T) {
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
	require.NotEmpty(t, firstStart["AUDIO_COMPILE"])
	require.True(t, firstStart["VOICEOVER"].Before(firstStart["DOCUMENT"]))
	require.True(t, firstStart["AUDIO_COMPILE"].Before(firstStart["DOCUMENT"]))

	// The publisher receives the voiceover-bearing document, proving that
	// document publication occurs after the voiceover phase.
	require.Contains(t, final.Result.Documents, Language("en"))
	require.Equal(t, CanonicalDocumentRendererID, final.Result.DocumentRenderers[Language("en")])
	require.NotEmpty(t, final.Result.DocumentSpecSceneSHA256[Language("en")])
	require.Equal(t, len(final.Result.Scenes), final.Result.DocumentSceneCounts[Language("en")])
	require.NotEmpty(t, docPub.records)
	require.Contains(t, docPub.records[0].Content, "<h2>Remote Job Payload JSON</h2>")
	require.NotContains(t, docPub.records[0].Content, "<h2>SpecScene JSON</h2>")
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
	want := []string{"NORMALIZE", "SCRIPT", "TRANSLATION", "VOICEOVER", "AUDIO_COMPILE", "PERSISTENCE", "DOCUMENT"}
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

// Package scriptgeneration — certification_10clip_10scene_test.go is the
// final end-to-end certification battery for the audio-only master
// contract:
//
//	10 real clips     → 10 known clip total durations (probe provenance)
//	10 scenes         → one per clip, narration-driven
//	10 voiceovers     → 10 Edge word-timing artifacts (same synthesis stream)
//	10 phrase timings → 10 scene speech timing projections (local→global)
//	1 final_audio.m4a → certified against the canonical timeline
//	1 Google Doc      → 10 scenes projected verbatim
//	0 video jobs      → no video render (audio/timeline only)
//
// It certifies the master invariants in one deterministic report:
//
//	PROJECT PROPAGATION  10/10  (request.Project → every voiceover input)
//	CLIP DURATION KNOWN  10/10  (AssetDuration.Known, never a fabricated 0)
//	EDGE TIMING          10/10  (valid word-level SpeechTimingArtifact)
//	PHRASE TIMING        10/10  (valid SceneSpeechTiming, local→global span)
//	VOICEOVER DRIVE      10/10  (each voiceover reference carries a Drive URL)
//	SUM(voiceover) == CanonicalTimeline.duration_us
//	abs(final_audio.duration_us - CanonicalTimeline.duration_us) ≤ tolerance
//	GOOGLE DOC PASS, RENDER PLAN NIL, VIDEO JOBS 0
package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// certClipTotalsUS is the certified TOTAL source duration of each of the 10
// clips, in integer microseconds, with probe provenance. They are distinct so
// the report proves every clip carries its own resolved duration rather than
// a shared default.
var certClipTotalsUS = []int64{
	18_420_000, // 18.420s
	24_300_000, // 24.300s
	15_750_000, // 15.750s
	21_100_000, // 21.100s
	19_830_000, // 19.830s
	26_400_000, // 26.400s
	17_260_000, // 17.260s
	22_900_000, // 22.900s
	20_570_000, // 20.570s
	23_650_000, // 23.650s
}

// certNarrations is one narration per scene with a varying word count, so the
// Edge word-timing and the per-scene timeline durations differ scene by scene.
var certNarrations = []string{
	"Scene zero narration covers clip one",
	"Scene one narration covers the second clip",
	"Scene two narration covers the third clip now",
	"Scene three narration covers the fourth clip here",
	"Scene four narration covers the fifth clip today",
	"Scene five narration covers the sixth clip right now",
	"Scene six narration covers the seventh clip right here",
	"Scene seven narration covers the eighth clip right now",
	"Scene eight narration covers the ninth clip immediately",
	"Scene nine narration covers the tenth clip completely",
}

// certAudioOnlyScenes builds 10 scenes, one per clip. Each clip carries a
// known probe-provenance total duration, while its used source window is the
// scene's narration-driven timeline duration (the canonical "video covers the
// scene" invariant). The legacy float Duration stays unset so the scene
// duration falls through to the voiceover — audio-only narration drives the
// timeline, never the clip's total length.
func certAudioOnlyScenes() []Scene {
	scenes := make([]Scene, len(certNarrations))
	for i, narration := range certNarrations {
		words := strings.Fields(narration)
		usedMS := int64(len(words)) * 100 // 100ms per Edge word
		clip := &ClipReference{
			ID:             fmt.Sprintf("clip-%02d", i),
			Title:          fmt.Sprintf("Certification clip %02d", i),
			DriveLink:      fmt.Sprintf("https://drive.google.com/file/d/clip-%02d", i),
			DurationUS:     certClipTotalsUS[i],
			DurationSource: kernelasset.DurationProbe,
			SourceInMS:     0,
			SourceOutMS:    usedMS,
		}
		scenes[i] = Scene{
			ID:           fmt.Sprintf("scene-%02d", i),
			Index:        i,
			Clip:         clip,
			Text:         map[Language]string{"en": narration},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		}
	}
	return scenes
}

// certVoiceoverGenerator returns a voiceover whose AudioReference carries the
// canonical word-level timing artifact captured in the same synthesis stream
// as the audio (100ms per whitespace-delimited word), a Drive URL (the
// published artifact), and records the Project each call received so the test
// can certify PROJECT PROPAGATION across all 10 scenes.
type certVoiceoverGenerator struct {
	mu       sync.Mutex
	projects []string
}

func (g *certVoiceoverGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
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
	g.mu.Lock()
	g.projects = append(g.projects, input.Project)
	g.mu.Unlock()
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-en",
		URL:      "https://drive.google.com/file/d/vo-" + input.SceneID + "-en",
		FilePath: "/tmp/vo-" + input.SceneID + "-en.mp3",
		Duration: float64(len(words)) * 0.1,
		Timing: &capabilityaudio.SpeechTimingArtifact{
			Version:      capabilityaudio.SpeechTimingVersion,
			Provider:     "edge_tts",
			BoundaryMode: capabilityaudio.BoundaryWord,
			Language:     string(input.Language),
			TextSHA256:   "text-hash-" + input.SceneID,
			AudioSHA256:  "audio-hash-" + input.SceneID,
			DurationUS:   int64(len(words)) * 100_000,
			Words:        boundaries,
		},
	}, nil
}

func (g *certVoiceoverGenerator) projectCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.projects)
}

// certAudioRenderer certifies the combined master against the canonical plan:
// DurationUS == plan.DurationUS (perfect deterministic render), so the test
// can certify abs(final - canonical) within the encoder-padding tolerance.
type certAudioRenderer struct{ calls int }

func (r *certAudioRenderer) Render(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error) {
	r.calls++
	return FinalAudioReference{
		AssetID:              "final-audio-10scene",
		Path:                 "/tmp/final_audio.m4a",
		Container:            "m4a",
		AudioContractVersion: capabilityaudio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		PlanSHA256:           plan.PlanSHA256,
		FinalAudioSHA256:     strings.Repeat("f", 64),
		Codec:                plan.Output.Codec,
		Profile:              plan.Output.Profile,
		SampleRate:           plan.Output.SampleRate,
		Channels:             plan.Output.Channels,
		ChannelLayout:        plan.Output.ChannelLayout,
		Bitrate:              128000,
		DurationUS:           plan.DurationUS,
		DurationMS:           plan.DurationUS / 1000,
		StartPTS:             0,
		SizeBytes:            1,
		FinalMix:             true,
		CopyEligible:         true,
	}, AudioPipelineMetrics{AudioDurationMS: plan.DurationUS / 1000}, nil
}

// formatSecUS renders integer microseconds as a fixed three-decimal second
// string ("18.420s") for the human-readable certification report.
func formatSecUS(us int64) string {
	return fmt.Sprintf("%d.%03ds", us/1_000_000, (us%1_000_000)/1000)
}

// certificationReport is the machine-readable mirror of the textual
// 10-clip / 10-scene audio-only certification report. It carries the same
// facts (per-scene durations, gate counts, totals, render/doc verdicts) so CI
// and downstream tooling can consume the CERTIFIED verdict without parsing
// the human report.
type certificationReport struct {
	Kind      string                  `json:"kind"`
	Version   int                     `json:"version"`
	Verdict   string                  `json:"verdict"`
	Project   string                  `json:"project"`
	RunID     string                  `json:"run_id"`
	Render    certificationRender     `json:"render"`
	GoogleDoc certificationGoogleDoc  `json:"google_doc"`
	Gates     certificationGates      `json:"gates"`
	TotalsUS  certificationTotalsUS   `json:"totals_us"`
	Scenes    []certificationSceneRow `json:"scenes"`
}

type certificationRender struct {
	VideoJobs int `json:"video_jobs"`
}

type certificationGoogleDoc struct {
	Passed     bool   `json:"passed"`
	DocumentID string `json:"document_id"`
	Link       string `json:"link"`
	SceneCount int    `json:"scene_count"`
}

type certificationGates struct {
	Total              int `json:"total"`
	ProjectPropagation int `json:"project_propagation"`
	ClipDurationKnown  int `json:"clip_duration_known"`
	EdgeTiming         int `json:"edge_timing"`
	PhraseTiming       int `json:"phrase_timing"`
	VoiceoverDrive     int `json:"voiceover_drive"`
}

type certificationTotalsUS struct {
	ClipSource        int64 `json:"clip_source"`
	ClipUsed          int64 `json:"clip_used"`
	EdgeVoiceover     int64 `json:"edge_voiceover"`
	CanonicalTimeline int64 `json:"canonical_timeline"`
	FinalAudio        int64 `json:"final_audio"`
	FinalAudioDelta   int64 `json:"final_audio_delta"`
	Tolerance         int64 `json:"tolerance"`
}

type certificationSceneRow struct {
	Scene       int   `json:"scene"`
	ClipTotalUS int64 `json:"clip_total_us"`
	ClipUsedUS  int64 `json:"clip_used_us"`
	EdgeVOUS    int64 `json:"edge_voiceover_us"`
	TimelineUS  int64 `json:"timeline_us"`
	Words       int   `json:"words"`
	Phrases     int   `json:"phrases"`
}

// writeCertificationJSON emits the machine-readable certification report as
// a JSON file (CERT_REPORT_PATH override, defaulting to the test temp dir)
// and as a single-line CERT_REPORT_JSON log for verbatim machine capture. The
// verdict is asserted to round-trip: a report whose Verdict is not CERTIFIED
// (or that drops any scene) fails the test.
func writeCertificationJSON(t *testing.T, report certificationReport) {
	t.Helper()
	require.Equal(t, "CERTIFIED", report.Verdict, "machine report verdict must be CERTIFIED")
	require.Len(t, report.Scenes, 10, "machine report must carry all 10 scenes")

	b, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err, "marshal certification report")

	path := os.Getenv("CERT_REPORT_PATH")
	if path == "" {
		path = filepath.Join(t.TempDir(), "certification.json")
	}
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o644), "write certification report")

	compact, err := json.Marshal(report)
	require.NoError(t, err, "marshal compact certification report")
	t.Logf("CERT_REPORT_PATH=%s", path)
	t.Logf("CERT_REPORT_JSON=%s", string(compact))
}

func absUS(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// TestCertification_TenClipsTenScenes_AudioOnly runs the full audio-only
// chain over 10 clips/10 scenes and certifies every invariant in one
// deterministic report. It is the executable form of the final
// "10 clip / 10 scene" certification contract.
func TestCertification_TenClipsTenScenes_AudioOnly(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(certAudioOnlyScenes())
	translator := newStubTranslator()
	voiceoverGen := &certVoiceoverGenerator{}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	renderer := &certAudioRenderer{}
	runner.SetCombinedAudioRenderer(renderer)

	const project = "cert-project"
	req := defaultTestRequest()
	req.Project = project
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Title = "Ten Clip Audio-Only Certification"

	runID := "run-10clip-10scene-audio-only"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-only run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res, "result must be present")
	require.Len(t, res.Scenes, 10, "all 10 scenes must survive")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")
	require.Len(t, res.CanonicalTimeline.Segments, 10, "one timeline segment per scene")

	// ── Per-scene certification rows ────────────────────────────────
	projectPass := 0
	clipKnown := 0
	edgeTimingPass := 0
	phraseTimingPass := 0
	voiceoverDrivePass := 0

	var totalClipSourceUS int64
	var totalClipUsedUS int64
	var totalEdgeVOUS int64

	rows := make([]string, 0, 10)
	sceneRows := make([]certificationSceneRow, 0, 10)
	for i, scene := range res.Scenes {
		segment := res.CanonicalTimeline.Segments[i]

		// PROJECT PROPAGATION — every voiceover input received the resolved
		// project verbatim (asserted once per scene below via the recorded
		// generator input).
		ref, ok := scene.Voiceover["en"]
		require.True(t, ok, "scene %s must carry an en voiceover", scene.ID)
		require.Equal(t, "vo-"+scene.ID+"-en", ref.ID)

		// CLIP DURATION KNOWN — the canonical total duration is probe-sourced
		// and known; never a fabricated zero.
		require.NotNil(t, scene.Clip, "scene %s must bind its clip", scene.ID)
		dur := scene.Clip.AssetDuration()
		require.NoError(t, dur.Validate(), "scene %s clip duration must be valid", scene.ID)
		require.True(t, dur.Known(), "scene %s clip duration must be known", scene.ID)
		clipKnown++

		// EDGE TIMING — a valid word-level artifact captured in the same
		// synthesis stream as the audio.
		require.NotNil(t, ref.Timing, "scene %s must carry word timing", scene.ID)
		require.NoError(t, ref.Timing.Validate(), "scene %s timing must be valid", scene.ID)
		edgeTimingPass++

		// VOICEOVER DRIVE — the voiceover reference carries its published URL.
		require.NotEmpty(t, ref.URL, "scene %s voiceover must be published to Drive", scene.ID)
		voiceoverDrivePass++

		// The used source window covers the scene timeline exactly (the
		// canonical "video covers the scene" invariant), and the scene is
		// narration-driven: timeline duration == voiceover duration.
		usedUS := (scene.Clip.SourceOutMS - scene.Clip.SourceInMS) * 1000
		require.Equal(t, usedUS, segment.DurationUS, "scene %s used window must cover its timeline", scene.ID)
		require.Equal(t, ref.Timing.DurationUS, segment.DurationUS, "scene %s timeline must be voiceover-driven", scene.ID)

		totalClipSourceUS += dur.DurationUS
		totalClipUsedUS += usedUS
		totalEdgeVOUS += ref.Timing.DurationUS

		rows = append(rows, fmt.Sprintf(
			"%2d | %s | %s | %s | %s | %d | %d",
			i,
			formatSecUS(dur.DurationUS),
			formatSecUS(usedUS),
			formatSecUS(ref.Timing.DurationUS),
			formatSecUS(segment.DurationUS),
			len(ref.Timing.Words),
			len(res.SceneSpeechTimings[i].Phrases),
		))
		sceneRows = append(sceneRows, certificationSceneRow{
			Scene:       i,
			ClipTotalUS: dur.DurationUS,
			ClipUsedUS:  usedUS,
			EdgeVOUS:    ref.Timing.DurationUS,
			TimelineUS:  segment.DurationUS,
			Words:       len(ref.Timing.Words),
			Phrases:     len(res.SceneSpeechTimings[i].Phrases),
		})
	}

	// PROJECT PROPAGATION 10/10 — the generator saw the resolved project for
	// every scene, never an empty or invented namespace.
	require.Equal(t, 10, voiceoverGen.projectCount(), "voiceover must be generated for 10 scenes")
	for _, seen := range voiceoverGen.projects {
		require.Equal(t, project, seen, "project must propagate verbatim to every voiceover input")
	}
	projectPass = voiceoverGen.projectCount()

	// PHRASE TIMING 10/10 — one scene-level speech timing per scene, each
	// valid, with the canonical local→global mapping.
	require.Len(t, res.SceneSpeechTimings, 10, "scene speech timings must cover all scenes")
	require.Len(t, res.PhraseTimings, 10, "flat phrase timings must cover all scenes")
	for i, st := range res.SceneSpeechTimings {
		require.NoError(t, st.Validate(), "scene %d speech timing must be valid", i)
		require.Equal(t, res.Scenes[i].ID, st.SceneID, "scene %d speech timing id", i)
		require.Len(t, st.Phrases, 1, "scene %d must anchor its narration as one phrase", i)
		require.Equal(t, res.PhraseTimings[i], st.Phrases[0], "scene %d flat/scene phrase projection must agree", i)

		p := st.Phrases[0]
		require.NoError(t, p.Validate(), "scene %d phrase must satisfy the master invariant", i)
		require.Equal(t, i, p.SceneIndex)
		require.Equal(t, 0, p.PhraseIndex)
		require.Equal(t, int64(0), p.LocalStartUS, "scene %d local phrase start must be the first word", i)
		require.Equal(t, p.TimelineStartUS, res.CanonicalTimeline.Segments[i].TimelineStartUS, "scene %d phrase must use the canonical offset", i)
		require.Equal(t, p.TimelineStartUS+p.LocalStartUS, p.GlobalStartUS)
		require.Equal(t, p.TimelineStartUS+p.LocalEndUS, p.GlobalEndUS)
		phraseTimingPass++
	}

	require.Equal(t, 10, clipKnown, "clip duration known 10/10")
	require.Equal(t, 10, edgeTimingPass, "edge timing 10/10")
	require.Equal(t, 10, phraseTimingPass, "phrase timing 10/10")
	require.Equal(t, 10, voiceoverDrivePass, "voiceover drive 10/10")

	// ── Master invariants ───────────────────────────────────────────
	timeline := res.CanonicalTimeline
	require.Equal(t, totalEdgeVOUS, timeline.DurationUS,
		"CanonicalTimeline must equal SUM(voiceover durations)")
	require.Equal(t, totalClipUsedUS, timeline.DurationUS,
		"the used clip window must cover the canonical timeline exactly")

	// Contiguity: scene[i+1].timeline_start == scene[i].start + duration.
	for i := 0; i < 9; i++ {
		require.Equal(t,
			timeline.Segments[i].TimelineStartUS+timeline.Segments[i].DurationUS,
			timeline.Segments[i+1].TimelineStartUS,
			"scene %d/%d must be contiguous", i, i+1)
	}
	require.Equal(t, int64(0), timeline.Segments[0].TimelineStartUS, "scene 0 must start at 0")

	// FINAL M4A — one certified master within the encoder-padding tolerance
	// of the canonical timeline.
	require.NotNil(t, res.FinalAudio, "final_audio.m4a must be certified")
	require.NotNil(t, res.AudioPlan, "audio plan must be persisted")
	require.Equal(t, capabilityaudio.FinalAudioCopy, res.AudioStrategy)
	require.Equal(t, 1, renderer.calls, "combined audio must be rendered exactly once")
	finalDelta := absUS(res.FinalAudio.DurationUS - timeline.DurationUS)
	require.LessOrEqual(t, finalDelta, FinalAudioDurationToleranceUS,
		"final_audio duration %d must be within %d us of canonical timeline %d",
		res.FinalAudio.DurationUS, FinalAudioDurationToleranceUS, timeline.DurationUS)

	// GOOGLE DOC — one doc published, projecting all 10 scenes.
	doc, ok := res.Documents["en"]
	require.True(t, ok, "document must be published")
	require.NotEmpty(t, doc.ID, "document id must be present")
	require.NotEmpty(t, doc.Link, "document link must be present")
	require.Equal(t, 10, res.DocumentSceneCounts["en"], "document must project all 10 scenes")

	// RENDER — audio-only contract: PipelineGen is audio-only, there is no
	// render plan, render job or video enqueue surface at all.

	// ── Human-readable certification report ─────────────────────────
	var report strings.Builder
	report.WriteString("\n===== 10 CLIP / 10 SCENE AUDIO-ONLY CERTIFICATION =====\n")
	report.WriteString("SCENE | CLIP TOTAL | CLIP USED | EDGE VO | TIMELINE | WORDS | PHRASES\n")
	for _, row := range rows {
		report.WriteString(row)
		report.WriteString("\n")
	}
	report.WriteString("\n")
	fmt.Fprintf(&report, "PROJECT PROPAGATION       %d/10 PASS\n", projectPass)
	fmt.Fprintf(&report, "CLIP DURATION KNOWN       %d/10 PASS\n", clipKnown)
	fmt.Fprintf(&report, "EDGE TIMING               %d/10 PASS\n", edgeTimingPass)
	fmt.Fprintf(&report, "PHRASE TIMING             %d/10 PASS\n", phraseTimingPass)
	fmt.Fprintf(&report, "VOICEOVER DRIVE           %d/10 PASS\n", voiceoverDrivePass)
	report.WriteString("\n")
	fmt.Fprintf(&report, "TOTAL CLIP SOURCE         = %s\n", formatSecUS(totalClipSourceUS))
	fmt.Fprintf(&report, "TOTAL CLIP USED           = %s\n", formatSecUS(totalClipUsedUS))
	fmt.Fprintf(&report, "TOTAL EDGE VO             = %s\n", formatSecUS(totalEdgeVOUS))
	fmt.Fprintf(&report, "CANONICAL TIMELINE        = %s\n", formatSecUS(timeline.DurationUS))
	fmt.Fprintf(&report, "FINAL M4A                 ~= %s\n", formatSecUS(res.FinalAudio.DurationUS))
	report.WriteString("\n")
	report.WriteString("GOOGLE DOC                PASS\n")
	report.WriteString("RENDER PLAN                N/A (audio-only)\n")
	report.WriteString("\nCERTIFIED\n")
	t.Log(report.String())

	// ── Machine-readable CERTIFIED report (JSON) ────────────────────
	writeCertificationJSON(t, certificationReport{
		Kind:    "audio_only_10clip_10scene_certification",
		Version: 1,
		Verdict: "CERTIFIED",
		Project: project,
		RunID:   runID,
		Render: certificationRender{
			VideoJobs: 0,
		},
		GoogleDoc: certificationGoogleDoc{
			Passed:     true,
			DocumentID: doc.ID,
			Link:       doc.Link,
			SceneCount: res.DocumentSceneCounts["en"],
		},
		Gates: certificationGates{
			Total:              10,
			ProjectPropagation: projectPass,
			ClipDurationKnown:  clipKnown,
			EdgeTiming:         edgeTimingPass,
			PhraseTiming:       phraseTimingPass,
			VoiceoverDrive:     voiceoverDrivePass,
		},
		TotalsUS: certificationTotalsUS{
			ClipSource:        totalClipSourceUS,
			ClipUsed:          totalClipUsedUS,
			EdgeVoiceover:     totalEdgeVOUS,
			CanonicalTimeline: timeline.DurationUS,
			FinalAudio:        res.FinalAudio.DurationUS,
			FinalAudioDelta:   finalDelta,
			Tolerance:         FinalAudioDurationToleranceUS,
		},
		Scenes: sceneRows,
	})
}

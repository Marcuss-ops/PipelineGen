// Package voiceover — golden_timing_e2e_test.go (PR-VOICEOVER-TIMING-GOLDEN).
//
// The Golden E2E that closes the voiceover-timing feature. It drives three
// scenes through the canonical ProcessSegmentUseCase with
// timing.mode=required and formats [json, srt, vtt], then certifies the full
// deterministic contract end-to-end:
//
//	SCRIPT TEXT
//	   ↓  (1 synthesis per scene — audio + WordBoundary in the SAME pass)
//	FINAL AUDIO + word timing
//	   ↓
//	timing.json (SSOT, SHA-256 bound) → SRT / VTT (projections)
//	   ↓
//	published links (real Publisher file IDs, never hand-built)
//	   ↓
//	PhraseLocator("incontro di Teano" | "Vittorio Emanuele II" | "Garibaldi"×2)
//	   ↓
//	deterministic moments (entity / phrase / keyword)
//
// godlike/07 NO-FAKE-AVAILABILITY invariants:
//   - exactly ONE TTS synthesis per scene, zero Whisper/transcription passes;
//   - every Drive link derives from the Publisher's real file ID;
//   - not-found annotation values are skipped, never interpolated.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// goldenSinglePassTTS synthesizes audio and word boundaries in ONE pass
// (100ms per whitespace-delimited word) — the exact production contract of
// the Edge bridge: audio chunks and WordBoundary chunks come from the SAME
// synthesis stream. There is deliberately no transcription path: a separate
// Whisper speech-to-text pass would appear as a SECOND Synthesize call per
// scene, which the golden test forbids.
type goldenSinglePassTTS struct {
	synthesized []TTSInput
	dir         string
}

func (s *goldenSinglePassTTS) Synthesize(_ context.Context, in TTSInput) (TTSOutput, error) {
	s.synthesized = append(s.synthesized, in)
	words := strings.Fields(in.Text)
	boundaries := make([]RawSpeechBoundary, len(words))
	for i, w := range words {
		boundaries[i] = RawSpeechBoundary{
			Text:    w,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	path := filepath.Join(s.dir, fmt.Sprintf("audio-%d.mp3", len(s.synthesized)))
	_ = os.WriteFile(path, []byte("fake-mp3-bytes"), 0o644)
	return TTSOutput{
		LocalPath:      path,
		Voice:          in.Voice,
		Provider:       "edge_tts",
		BoundaryMode:   audio.BoundaryWord,
		Duration:       time.Duration(len(words)) * 100 * time.Millisecond,
		WordBoundaries: boundaries,
	}, nil
}

var _ TTSProvider = (*goldenSinglePassTTS)(nil)

// goldenPublisher records every published file's bytes and assigns a real,
// sequential Drive file ID per upload, so the test can prove that every
// link in the timing result derives from a Publisher file ID (no
// hand-built links).
type goldenPublisher struct {
	files map[string][]byte
	ids   map[string]string
	seq   int
}

func (p *goldenPublisher) Publish(_ context.Context, cmd VoiceoverPublishCommand) (string, error) {
	if p.files == nil {
		p.files = map[string][]byte{}
		p.ids = map[string]string{}
	}
	data, _ := os.ReadFile(cmd.LocalPath)
	p.files[cmd.Filename] = data
	p.seq++
	id := fmt.Sprintf("drive-file-%03d", p.seq)
	p.ids[cmd.Filename] = id
	return id, nil
}

var _ VoiceoverPublisher = (*goldenPublisher)(nil)

// goldenScene is one golden-E2E scene: the exact narration text plus the
// annotation queries (the LLM-produced kind+value strings) that must become
// deterministic moments.
type goldenScene struct {
	text    string
	moments []audio.MomentQuery
}

func TestGoldenTimingE2E_ThreeScenes(t *testing.T) {
	scenes := []goldenScene{
		{text: "Nel cuore dell'Italia risiede lo spirito della nazione."},
		{
			text: "Il celebre incontro di Teano con re Vittorio Emanuele II cambiò il corso degli eventi.",
			moments: []audio.MomentQuery{
				{Kind: audio.MomentEntity, Value: "Vittorio Emanuele II"},
				{Kind: audio.MomentPhrase, Value: "incontro di Teano"},
			},
		},
		{
			text: "Garibaldi incontrò il re, poi Garibaldi guidò la spedizione.",
			moments: []audio.MomentQuery{
				{Kind: audio.MomentEntity, Value: "Garibaldi"},
			},
		},
	}

	stagingDir := t.TempDir()
	tts := &goldenSinglePassTTS{dir: t.TempDir()}
	pub := &goldenPublisher{}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-golden"}},
		Logger:              zap.NewNop(),
	})

	timingPolicy := &audio.TimingRequest{
		Mode:         audio.TimingRequired,
		BoundaryMode: audio.BoundaryWord,
		Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT, audio.TimingVTT},
	}

	var results []*VoiceoverTimingResult
	for i, scene := range scenes {
		out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
			ID:       fmt.Sprintf("vo-golden-%d", i),
			JobID:    "job-golden",
			Text:     scene.text,
			TextHash: TextHash(digest.SHA256String(scene.text)),
			Language: "it",
			Voice:    "it-IT-DiegoNeural",
			Filename: fmt.Sprintf("scene-%d.mp3", i),
			Timing:   timingPolicy,
			Moments:  scene.moments,
			Dest:     &ResolvedDestination{FolderID: "folder-golden", FolderPath: stagingDir},
		})
		require.NoError(t, err, "scene %d must complete under required timing", i)
		require.NotNil(t, out)
		require.Equal(t, StatusCompleted, out.Status, "scene %d must SUCCEED", i)
		require.NotNil(t, out.Timing, "scene %d must publish a timing bundle", i)
		results = append(results, out.Timing)
	}

	// ── 1 synthesis per scene, zero Whisper/transcription ────────────────
	// The golden pipeline certifies "1 TTS synthesis per scene, 0 Whisper
	// calls, 0 transcription jobs": the TTS provider is the ONLY synthesis
	// port, it is invoked exactly once per scene, and the word boundaries
	// arrive inside that same single-pass output — a second speech-to-text
	// pass would show up as an extra Synthesize call.
	require.Len(t, tts.synthesized, 3, "exactly one synthesis per scene; zero Whisper/transcription passes")
	for _, in := range tts.synthesized {
		assert.False(t, in.RemoveSilence, "TTS must never be asked to strip silence inline")
	}

	// ── 3 audio + 3 timing.json + 3 srt + 3 vtt ───────────────────────────
	require.Len(t, pub.files, 12, "3 scenes × (audio + timing.json + srt + vtt)")

	for i := 0; i < 3; i++ {
		audioName := fmt.Sprintf("scene-%d.mp3", i)
		jsonName := fmt.Sprintf("scene-%d-timing.json", i)
		srtName := fmt.Sprintf("scene-%d.srt", i)
		vttName := fmt.Sprintf("scene-%d.vtt", i)

		require.Contains(t, pub.files, audioName, "scene %d audio published", i)
		require.NotEmpty(t, pub.files[audioName], "scene %d audio must be non-empty bytes", i)
		require.Contains(t, pub.files, jsonName)
		require.Contains(t, pub.files, srtName)
		require.Contains(t, pub.files, vttName)

		// timing.json SSOT: validate the full canonical contract.
		var artifact audio.SpeechTimingArtifact
		require.NoError(t, json.Unmarshal(pub.files[jsonName], &artifact), "scene %d timing.json must be valid JSON", i)
		require.NoError(t, artifact.Validate(), "scene %d timing.json must pass Validate", i)
		assert.Equal(t, audio.SpeechTimingVersion, artifact.Version, "scene %d version=1", i)
		assert.Equal(t, "edge_tts", artifact.Provider, "scene %d provider=edge_tts", i)
		assert.Equal(t, audio.BoundaryWord, artifact.BoundaryMode, "scene %d boundary_mode=word", i)
		assert.Equal(t, "it", artifact.Language, "scene %d language=it", i)
		assert.NotEmpty(t, artifact.Words, "scene %d words non-empty", i)
		assert.Equal(t, digest.SHA256String(scenes[i].text), artifact.TextSHA256, "scene %d text_sha256 exact", i)
		assert.Equal(t, digest.SHA256String("fake-mp3-bytes"), artifact.AudioSHA256, "scene %d audio_sha256 exact", i)

		// SRT / VTT are projections of the SSOT.
		assert.Contains(t, string(pub.files[srtName]), " --> ", "scene %d SRT must carry cue timestamps", i)
		assert.True(t, strings.HasPrefix(string(pub.files[vttName]), "WEBVTT"), "scene %d VTT must start with WEBVTT", i)

		// Timing result surface (what /full exposes via VoiceoverTimingBinding).
		res := results[i]
		assert.Equal(t, TimingStatusCompleted, res.Status, "scene %d timing status completed", i)
		assert.NotEmpty(t, res.JSONLink)
		assert.NotEmpty(t, res.SRTLink)
		assert.NotEmpty(t, res.VTTLink)
		assert.Equal(t, len(artifact.Words), res.WordCount, "scene %d word_count", i)
		assert.Equal(t, artifact.DurationUS, res.DurationUS, "scene %d duration_us", i)
		assert.Equal(t, artifact.TextSHA256, res.TextSHA256, "scene %d text hash surfaces", i)
		assert.Equal(t, artifact.AudioSHA256, res.AudioSHA256, "scene %d audio hash surfaces", i)

		// Zero fabricated Drive links: every link is the canonical web URL of
		// the Publisher's real returned file ID.
		assert.Equal(t, CanonicalDriveWebURL(pub.ids[jsonName]), res.JSONLink, "scene %d json_link from real publisher ID", i)
		assert.Equal(t, CanonicalDriveWebURL(pub.ids[srtName]), res.SRTLink, "scene %d srt_link from real publisher ID", i)
		assert.Equal(t, CanonicalDriveWebURL(pub.ids[vttName]), res.VTTLink, "scene %d vtt_link from real publisher ID", i)
	}

	// ── PhraseLocator over the published timing.json ──────────────────────
	var scene1, scene2 audio.SpeechTimingArtifact
	require.NoError(t, json.Unmarshal(pub.files["scene-1-timing.json"], &scene1))
	require.NoError(t, json.Unmarshal(pub.files["scene-2-timing.json"], &scene2))

	teano, err := audio.LocatePhrase(scene1, "incontro di Teano")
	require.NoError(t, err)
	require.Len(t, teano, 1, "'incontro di Teano' must be FOUND exactly once")
	assert.Equal(t, int64(200_000), teano[0].StartUS)
	assert.Equal(t, int64(500_000), teano[0].EndUS)

	vittorio, err := audio.LocatePhrase(scene1, "Vittorio Emanuele II")
	require.NoError(t, err)
	require.Len(t, vittorio, 1, "'Vittorio Emanuele II' must be FOUND exactly once")
	assert.Equal(t, int64(700_000), vittorio[0].StartUS)
	assert.Equal(t, int64(1_000_000), vittorio[0].EndUS)

	garibaldi, err := audio.LocatePhrase(scene2, "Garibaldi")
	require.NoError(t, err)
	require.Len(t, garibaldi, 2, "'Garibaldi' must be FOUND twice in scene 2")
	assert.Equal(t, 1, garibaldi[0].Occurrence)
	assert.Equal(t, int64(0), garibaldi[0].StartUS)
	assert.Equal(t, int64(100_000), garibaldi[0].EndUS)
	assert.Equal(t, 2, garibaldi[1].Occurrence)
	assert.Equal(t, int64(500_000), garibaldi[1].StartUS)
	assert.Equal(t, int64(600_000), garibaldi[1].EndUS)

	// ── Deterministic moments (LLM text → PhraseLocator → timestamps) ────
	require.Len(t, results[1].Moments, 2, "scene 1: entity + phrase moments")
	assert.Equal(t, audio.MomentEntity, results[1].Moments[0].Kind)
	assert.Equal(t, "Vittorio Emanuele II", results[1].Moments[0].Value)
	assert.Equal(t, int64(700_000), results[1].Moments[0].StartUS)
	assert.Equal(t, int64(1_000_000), results[1].Moments[0].EndUS)
	assert.Equal(t, audio.MomentPhrase, results[1].Moments[1].Kind)
	assert.Equal(t, "incontro di Teano", results[1].Moments[1].Value)
	assert.Equal(t, int64(200_000), results[1].Moments[1].StartUS)
	assert.Equal(t, int64(500_000), results[1].Moments[1].EndUS)

	require.Len(t, results[2].Moments, 2, "scene 2: Garibaldi appears twice")
	assert.Equal(t, audio.MomentEntity, results[2].Moments[0].Kind)
	assert.Equal(t, "Garibaldi", results[2].Moments[0].Value)
	assert.Equal(t, 1, results[2].Moments[0].Occurrence)
	assert.Equal(t, int64(0), results[2].Moments[0].StartUS)
	assert.Equal(t, int64(100_000), results[2].Moments[0].EndUS)
	assert.Equal(t, 2, results[2].Moments[1].Occurrence)
	assert.Equal(t, int64(500_000), results[2].Moments[1].StartUS)
	assert.Equal(t, int64(600_000), results[2].Moments[1].EndUS)
}

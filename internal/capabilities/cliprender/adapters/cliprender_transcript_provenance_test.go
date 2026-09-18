package adapters

// cliprender_transcript_provenance_test.go — the "a forced decode may never be
// recorded as a detection" contract.
//
// faster-whisper ECHOES the language it was told to use, so `detected_language`
// from a forced decode is the REQUEST, not evidence about the audio. The bridge
// therefore emits three distinct facts — the decode language, the unforced
// detection, and whether a language was forced — and these cases pin that the Go
// side keeps them apart end to end: through the subprocess contract, into the
// typed result, and into the persisted provenance of the canonical text track.
//
// The bridge and FFmpeg are doubled as shell scripts, so this runs everywhere
// with no Whisper model, no GPU and no network.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// requireShell returns the POSIX shell the subprocess doubles run under, or
// skips: these cases describe a POSIX subprocess contract.
func requireShell(t *testing.T) string {
	t.Helper()
	shell, err := exec.LookPath("/bin/sh")
	if err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping the subprocess contract test", err)
	}
	return shell
}

// fakeFFmpeg writes an executable that satisfies the decoder's argv contract by
// emitting no PCM at all (an empty source is a legal input; the bridge, not
// FFmpeg, is the subject here).
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

// fakeBridge writes a script that prints the given JSON payload on stdout,
// standing in for scripts/bridges/whisper_transcriber.py.
func fakeBridge(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bridge.sh")
	body := "#!/bin/sh\ncat >/dev/null\necho '" + payload + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake bridge: %v", err)
	}
	return path
}

// transcriberWith builds the streaming transcriber against the doubles, so the
// ONLY thing under test is the bridge→result mapping.
func transcriberWith(t *testing.T, payload string) *ClipRenderStreamingTranscriber {
	t.Helper()
	return &ClipRenderStreamingTranscriber{
		pythonBin:  requireShell(t),
		scriptPath: fakeBridge(t, payload),
		ffmpegPath: fakeFFmpeg(t),
		log:        zap.NewNop(),
	}
}

func transcribeWithPayload(t *testing.T, payload string) *cliprender.TranscriptResult {
	t.Helper()
	source := &cliprender.MaterializedAsset{AssetID: "source-asset-1", LocalPath: filepath.Join(t.TempDir(), "source.mp4")}
	if err := os.WriteFile(source.LocalPath, []byte("fake media"), 0o644); err != nil {
		t.Fatalf("write fake source: %v", err)
	}
	res, err := transcriberWith(t, payload).TranscribeStream(context.Background(), source, "it")
	if err != nil {
		t.Fatalf("TranscribeStream: %v", err)
	}
	return res
}

// TestStreamingTranscriber_ForcedEchoIsNotADetection is the anti-tautology
// proof: the bridge was told "it" and echoes "it" as the decode language, while
// the genuine detection says "en". The result must carry BOTH, and must not let
// the echo stand in for the detection — that substitution is what turned the
// caller's language check into a check of its own request.
func TestStreamingTranscriber_ForcedEchoIsNotADetection(t *testing.T) {
	res := transcribeWithPayload(t, `{"text":"ciao a tutti","language":"it","detected_language":"en","language_forced":true,"confidence":0.91,"duration_ms":4200,"cues":[{"start_ms":0,"end_ms":900,"text":"ciao a tutti"}]}`)

	if res.Language != "it" {
		t.Errorf("Language = %q, want %q (the language of the TEXT)", res.Language, "it")
	}
	if res.DetectedLanguage != "en" {
		t.Errorf("DetectedLanguage = %q, want %q (what the model heard) — the forced echo must never be reported as a detection", res.DetectedLanguage, "en")
	}
	if !res.LanguageForced {
		t.Error("LanguageForced = false, want true (the caller forced this decode)")
	}
	if res.Text == "" || len(res.Cues) != 1 {
		t.Errorf("text/cues not carried through: text=%q cues=%d", res.Text, len(res.Cues))
	}
}

// TestStreamingTranscriber_AutoDetectionIsTheDetection pins the unforced case:
// with nothing forced, the decode language IS the detection and the result says
// so — no extra inference, no divergence between the two fields.
func TestStreamingTranscriber_AutoDetectionIsTheDetection(t *testing.T) {
	res := transcribeWithPayload(t, `{"text":"hello","language":"en","detected_language":"en","language_forced":false,"duration_ms":1000}`)

	if res.Language != "en" || res.DetectedLanguage != "en" {
		t.Errorf("language=%q detected=%q, want both %q", res.Language, res.DetectedLanguage, "en")
	}
	if res.LanguageForced {
		t.Error("LanguageForced = true, want false on the auto-detect path")
	}
}

// TestStreamingTranscriber_AcceptsOlderBridgePayload pins forward-compatibility
// with a bridge that predates the split: it emits only `detected_language`, and
// the adapter must still produce a usable result instead of erroring on the
// missing fields. The documented fallback is that field.
func TestStreamingTranscriber_AcceptsOlderBridgePayload(t *testing.T) {
	res := transcribeWithPayload(t, `{"text":"hello","detected_language":"fr","confidence":0.5,"duration_ms":800}`)

	if res.Language != "fr" {
		t.Errorf("Language = %q, want %q (fallback to the only language the bridge reported)", res.Language, "fr")
	}
	if res.DetectedLanguage != "fr" {
		t.Errorf("DetectedLanguage = %q, want %q", res.DetectedLanguage, "fr")
	}
	if res.LanguageForced {
		t.Error("an older payload cannot claim a forced decode; LanguageForced must stay false")
	}
}

// recordingTrackRepo captures the rows persistResult writes.
type recordingTrackRepo struct {
	detail.TextTrackRepository
	tracks []detail.TextTrack
}

func (r *recordingTrackRepo) UpsertBatch(_ context.Context, tracks []detail.TextTrack) error {
	r.tracks = append(r.tracks, tracks...)
	return nil
}

// TestPersistResult_ForcedDecodeIsNotTheOriginalTranscript is the provenance
// proof at the write boundary: a forced Italian decode of English audio is
// persisted under language "it" (that IS the language of its text) but with
// source_language_code "en" and is_original false. Recording it as the clip's
// original transcript would hand fabricated text to every later reader that
// asks "what does this clip say" — the translation source selector, the search
// index and the subtitle checks alike.
func TestPersistResult_ForcedDecodeIsNotTheOriginalTranscript(t *testing.T) {
	repo := &recordingTrackRepo{}
	resolver := &ClipRenderTranscriptResolver{repo: repo, log: zap.NewNop()}

	err := resolver.persistResult(context.Background(), "source-asset-1", &cliprender.MaterializedAsset{AssetID: "source-asset-1"},
		&cliprender.TranscriptResult{
			Language:         "it",
			DetectedLanguage: "en",
			LanguageForced:   true,
			Text:             "ciao a tutti",
			TextSHA256:       "hash",
			StreamSourceType: string(detail.TextSourceWhisper),
			Cues:             []cliprender.Cue{{StartMs: 0, EndMs: 900, Text: "ciao a tutti"}},
		})
	if err != nil {
		t.Fatalf("persistResult: %v", err)
	}
	if len(repo.tracks) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(repo.tracks))
	}
	track := repo.tracks[0]
	if track.LanguageCode != "it" {
		t.Errorf("language_code = %q, want %q (the language of the text)", track.LanguageCode, "it")
	}
	if track.SourceLanguageCode != "en" {
		t.Errorf("source_language_code = %q, want %q (the language of the audio)", track.SourceLanguageCode, "en")
	}
	if track.IsOriginal {
		t.Error("is_original = true, want false: a forced decode of other-language audio is not the clip's own transcript")
	}
}

// TestPersistResult_OwnLanguageDecodeIsTheOriginalTranscript pins the
// non-regression half: a decode in the audio's own language stays the original
// transcript of that language. A fix that marked everything non-original would
// break the translation source selection this provenance exists to protect.
func TestPersistResult_OwnLanguageDecodeIsTheOriginalTranscript(t *testing.T) {
	repo := &recordingTrackRepo{}
	resolver := &ClipRenderTranscriptResolver{repo: repo, log: zap.NewNop()}

	err := resolver.persistResult(context.Background(), "source-asset-1", &cliprender.MaterializedAsset{AssetID: "source-asset-1"},
		&cliprender.TranscriptResult{
			Language:         "en",
			DetectedLanguage: "en",
			Text:             "hello world",
			TextSHA256:       "hash",
			StreamSourceType: string(detail.TextSourceWhisper),
		})
	if err != nil {
		t.Fatalf("persistResult: %v", err)
	}
	if len(repo.tracks) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(repo.tracks))
	}
	track := repo.tracks[0]
	if track.LanguageCode != "en" || track.SourceLanguageCode != "en" {
		t.Errorf("language/source = %q/%q, want en/en", track.LanguageCode, track.SourceLanguageCode)
	}
	if !track.IsOriginal {
		t.Error("is_original = false, want true for a decode in the audio's own language")
	}
}

// TestSameLanguage_CanonicalComparison pins the comparison the provenance
// decision rests on: a format variant of one language ("EN" vs "en") is the
// same language, an unknown tag is not assumed equal, and an empty language
// only equals an empty language (never a concrete one).
func TestSameLanguage_CanonicalComparison(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{a: "en", b: "en", want: true},
		{a: "EN", b: "en", want: true},
		{a: " en ", b: "en", want: true},
		{a: "it", b: "en", want: false},
		{a: "", b: "en", want: false},
		{a: "", b: "", want: true},
		{a: "en", b: "not a language tag", want: false},
	}
	for _, tc := range cases {
		if got := sameLanguage(tc.a, tc.b); got != tc.want {
			t.Errorf("sameLanguage(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

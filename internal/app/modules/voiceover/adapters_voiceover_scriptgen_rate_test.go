package voiceover

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"go.uber.org/zap"
)

// capturingVoiceoverExecutor records the command the adapter forwards to the
// canonical per-item pipeline. It returns a minimal successful result so the
// adapter's post-pipeline mapping can complete without touching the network.
type capturingVoiceoverExecutor struct {
	cmd    *voiceover.GenerateVoiceoverItemCommand
	calls  int
	result *voiceover.VoiceoverItemResult
	err    error
}

func (e *capturingVoiceoverExecutor) Execute(_ context.Context, cmd *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	e.cmd = cmd
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	if e.result != nil {
		return e.result, nil
	}
	return &voiceover.VoiceoverItemResult{
		Status:        voiceover.StatusGenerated,
		LocalPath:     "/tmp/scriptgen-rate.mp3",
		DurationMs:    1234,
		ContentSHA256: "content-hash",
		ID:            "vo-rate",
	}, nil
}

func TestNormalizeTTSRate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		{"", "-20%"},
		{"   ", "-20%"},
		{"+10%", "+10%"},
		{"10%", "+10%"},
		{"-20%", "-20%"},
		{"+0%", "+0%"},
		{"0%", "+0%"},
		{"1.1x", "+10%"},
		{"1.1X", "+10%"},
		{"1.1", "+10%"},
		{"1.2", "+20%"},
		{"1.05x", "+5%"},
		{"1.05", "+5%"},
		{"1.01x", "+1%"},
		{"0.95x", "-5%"},
		{"1.15x", "+15%"},
		{"0.8x", "-20%"},
		{"0.5", "-50%"},
		{"2.0x", "+100%"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := normalizeTTSRate(tc.raw)
			if err != nil {
				t.Fatalf("normalizeTTSRate(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeTTSRate(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormalizeTTSRateRejectsOutOfRangeAndJunk(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"abc", "3.0x", "0.4x", "-80%", "+150%", "1.1xx", "x", "-1", "+", "%"} {
		t.Run(raw, func(t *testing.T) {
			if got, err := normalizeTTSRate(raw); err == nil {
				t.Fatalf("normalizeTTSRate(%q) = %q, want error", raw, got)
			}
		})
	}
}

func TestScriptVoiceoverGeneratorForwardsNormalizedRate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"1.1x", "+10%"},
		{"+10%", "+10%"},
		{"1.05x", "+5%"},
		{"0.8x", "-20%"},
		{"", "-20%"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			exec := &capturingVoiceoverExecutor{}
			g := NewScriptVoiceoverGenerator(exec, t.TempDir(), zap.NewNop())
			if _, err := g.Generate(context.Background(), scriptgen.VoiceoverInput{
				SceneID:  "scene-0",
				Language: "it",
				Text:     "ciao mondo",
				Project:  "project",
				TTSRate:  tc.raw,
			}); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if exec.cmd == nil {
				t.Fatal("executor did not receive a command")
			}
			if exec.cmd.TTSRate != tc.want {
				t.Fatalf("cmd.TTSRate = %q, want %q", exec.cmd.TTSRate, tc.want)
			}
		})
	}
}

func TestScriptVoiceoverGeneratorRejectsInvalidRateBeforeTTS(t *testing.T) {
	t.Parallel()

	exec := &capturingVoiceoverExecutor{}
	g := NewScriptVoiceoverGenerator(exec, t.TempDir(), zap.NewNop())
	_, err := g.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "scene-0",
		Language: "it",
		Text:     "ciao mondo",
		Project:  "project",
		TTSRate:  "3.0x",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid TTS rate") {
		t.Fatalf("Generate error = %v, want invalid TTS rate", err)
	}
	if exec.calls != 0 {
		t.Fatalf("executor called %d times, want 0", exec.calls)
	}
}

func TestNormalizedRateSharesCacheFingerprint(t *testing.T) {
	t.Parallel()

	multiplier, err := normalizeTTSRate("1.1x")
	if err != nil {
		t.Fatal(err)
	}
	percent, err := normalizeTTSRate("+10%")
	if err != nil {
		t.Fatal(err)
	}
	build := func(rate string) string {
		return voiceover.BuildVoiceoverContentFingerprintV4(
			"text-hash", "it", "voice", "provider", "folder", nil, false,
			voiceover.VoiceoverPostPolicy{
				Rate:    rate,
				Silence: capabilityaudio.SilenceRemovalPolicy{},
			},
		)
	}
	if fpMultiplier, fpPercent := build(multiplier), build(percent); fpMultiplier != fpPercent {
		t.Fatalf("equivalent rates produced different fingerprints: %q vs %q", fpMultiplier, fpPercent)
	}
	slower, err := normalizeTTSRate("-20%")
	if err != nil {
		t.Fatal(err)
	}
	if build(multiplier) == build(slower) {
		t.Fatal("different rates produced the same cache fingerprint")
	}
}

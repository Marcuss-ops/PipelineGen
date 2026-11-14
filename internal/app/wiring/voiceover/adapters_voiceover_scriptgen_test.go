package voiceover

import (
	"context"
	"errors"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

type retryVoiceoverExecutor struct {
	calls    int
	failures []error
}

func (f *retryVoiceoverExecutor) Execute(_ context.Context, _ *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	f.calls++
	if f.calls <= len(f.failures) {
		return nil, f.failures[f.calls-1]
	}
	return &voiceover.VoiceoverItemResult{
		Status:        voiceover.StatusCompleted,
		LocalPath:     "/tmp/audio.mp3",
		ContentSHA256: "sha256-audio",
		DurationMs:    1500,
	}, nil
}

func TestScriptVoiceoverGeneratorRetriesTransientTTSFailure(t *testing.T) {
	exec := &retryVoiceoverExecutor{failures: []error{
		&retry.TransientInfrastructureError{Err: errors.New("edge tts connection reset")},
	}}
	gen := NewScriptVoiceoverGenerator(exec, t.TempDir(), nil)

	ref, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "scene-02",
		Text:     "testo della scena",
		Language: "it",
	})
	if err != nil {
		t.Fatalf("Generate after a transient TTS failure: %v", err)
	}
	if exec.calls != 2 {
		t.Fatalf("executor calls = %d, want 2 (one failure + one successful retry)", exec.calls)
	}
	if ref.FilePath != "/tmp/audio.mp3" || ref.Duration <= 0 {
		t.Fatalf("AudioReference = %+v, want the successful retry result", ref)
	}
}

func TestScriptVoiceoverGeneratorDoesNotRetryPermanentTTSFailure(t *testing.T) {
	exec := &retryVoiceoverExecutor{failures: []error{errors.New("invalid voice for language")}}
	gen := NewScriptVoiceoverGenerator(exec, t.TempDir(), nil)

	if _, err := gen.Generate(context.Background(), scriptgen.VoiceoverInput{
		SceneID:  "scene-02",
		Text:     "testo della scena",
		Language: "it",
	}); err == nil {
		t.Fatal("Generate returned nil error for a permanent TTS failure")
	}
	if exec.calls != 1 {
		t.Fatalf("executor calls = %d, want 1 (permanent failures must not be retried)", exec.calls)
	}
}

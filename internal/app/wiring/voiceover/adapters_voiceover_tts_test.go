package voiceover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	voiceoversvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"go.uber.org/zap"
)

type cleanupMediaFake struct {
	duration time.Duration
	edits    []audio.AudioEdit
}

func (f *cleanupMediaFake) RemoveSilence(_ context.Context, _, output string) error {
	return os.WriteFile(output, []byte("cleaned-audio-bytes"), 0o644)
}

func (f *cleanupMediaFake) RemoveSilenceWithEdits(ctx context.Context, _, output string) ([]audio.AudioEdit, error) {
	if err := f.RemoveSilence(ctx, "", output); err != nil {
		return nil, err
	}
	return f.edits, nil
}

func (f *cleanupMediaFake) Probe(_ context.Context, _ string) (*mediaexec.MediaInfo, error) {
	return &mediaexec.MediaInfo{Duration: f.duration}, nil
}

func (f *cleanupMediaFake) MergeInputs(context.Context, []string, string) error { return nil }

func (f *cleanupMediaFake) RenderAudioPlan(context.Context, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, string) (audio.FinalAudioAsset, error) {
	return audio.FinalAudioAsset{}, nil
}

func TestUseCaseAudioAdapterHashesAndMeasuresCleanedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.mp3")
	if err := os.WriteFile(raw, []byte("raw-audio-bytes"), 0o644); err != nil {
		t.Fatalf("write raw audio: %v", err)
	}

	edits := []audio.AudioEdit{{
		SourceStartUS: 0,
		SourceEndUS:   120_000,
		OutputStartUS: 120_000,
		OutputEndUS:   120_000,
	}}
	adapter := NewUseCaseAudioAdapter(zap.NewNop(), &cleanupMediaFake{
		duration: 1875 * time.Millisecond,
		edits:    edits,
	})

	out, err := adapter.Process(context.Background(), voiceoversvc.AudioPostInput{
		LocalPath: raw,
		OutputDir: dir,
		Filename:  "voice.mp3",
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	cleaned := filepath.Join(dir, "cleaned_voice.mp3")
	if out.CleanedPath != cleaned {
		t.Fatalf("CleanedPath = %q, want %q", out.CleanedPath, cleaned)
	}
	wantHash, err := checksum.LegacyMD5File(cleaned)
	if err != nil {
		t.Fatalf("hash cleaned file: %v", err)
	}
	if out.ContentSHA256 != wantHash {
		t.Fatalf("ContentSHA256 = %q, want cleaned-file digest %q", out.ContentSHA256, wantHash)
	}
	if out.DurationUS != 1_875_000 {
		t.Fatalf("DurationUS = %d, want 1875000", out.DurationUS)
	}
	if len(out.EditMap) != 1 || out.EditMap[0] != edits[0] {
		t.Fatalf("EditMap = %+v, want %+v", out.EditMap, edits)
	}
}

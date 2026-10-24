package scriptgeneration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubFinalAudioPublisher implements FinalAudioPublisher with a fixed
// canonical outcome, proving the runner consumes the canonical asset ID
// instead of a local path.
type stubFinalAudioPublisher struct {
	result FinalAudioPublishResult
	err    error
	calls  int
}

func (s *stubFinalAudioPublisher) PublishFinalAudio(context.Context, string, Language, FinalAudioReference, string) (FinalAudioPublishResult, error) {
	s.calls++
	return s.result, s.err
}

func testRoutingContext() kernelscript.ArtifactRoutingContext {
	return kernelscript.ResolveArtifactRoutingContext("comici-sandler", "it", "explicit-vo-folder", "explicit-docs-folder")
}

func TestRunnerPublishFinalAudioSetsCanonicalAssetID(t *testing.T) {
	repo := newInMemRunRepository()
	runner := NewRunner(repo, newStubTextGenerator(defaultTestScenes()), newStubTranslator(), newStubVoiceoverGenerator(), newStubDocumentPublisher())
	runner.SetLogger(zap.NewNop())
	publisher := &stubFinalAudioPublisher{result: FinalAudioPublishResult{
		AssetID:     "final-audio-canonical-01k",
		DriveFileID: "abc",
		DriveLink:   "https://drive.google.com/file/d/abc",
	}}
	runner.SetFinalAudioPublisher(publisher)

	req := defaultTestRequest()
	req.SourceLanguage = "en"
	localAudio := filepath.Join(t.TempDir(), "final-audio.m4a")
	require.NoError(t, os.WriteFile(localAudio, []byte("audio"), 0o600))
	result := &GenerateResult{
		FinalAudio: &FinalAudioReference{
			AssetID:          localAudio,
			Path:             localAudio,
			FinalAudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			DurationMS:       1000,
		},
		AudioMetrics: &AudioPipelineMetrics{},
	}

	ok := runner.publishFinalAudio(context.Background(), "run-1", req, testRoutingContext(), NewExecutionContext("run-1", "corr-1"), result)
	require.True(t, ok, "publish must succeed")
	require.Equal(t, 1, publisher.calls)
	require.Equal(t, "final-audio-canonical-01k", result.FinalAudio.AssetID, "audio_asset_id must be the canonical asset ID, not the local path")
	require.Equal(t, "https://drive.google.com/file/d/abc", result.FinalAudio.DriveLink)
	require.Equal(t, "abc", result.FinalAudio.DriveFileID)
	require.Equal(t, localAudio, result.FinalAudio.Path, "local path must remain on the internal Path field")
}

func TestRunnerPublishFinalAudioFailsClosedOnEmptyCanonicalAssetID(t *testing.T) {
	repo := newInMemRunRepository()
	runner := NewRunner(repo, newStubTextGenerator(defaultTestScenes()), newStubTranslator(), newStubVoiceoverGenerator(), newStubDocumentPublisher())
	runner.SetLogger(zap.NewNop())
	publisher := &stubFinalAudioPublisher{result: FinalAudioPublishResult{DriveFileID: "abc", DriveLink: "https://drive.google.com/file/d/abc"}}
	runner.SetFinalAudioPublisher(publisher)

	req := defaultTestRequest()
	req.SourceLanguage = "en"
	localAudio := filepath.Join(t.TempDir(), "final-audio.m4a")
	require.NoError(t, os.WriteFile(localAudio, []byte("audio"), 0o600))
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: "run-2", Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	result := &GenerateResult{
		FinalAudio: &FinalAudioReference{
			AssetID:          localAudio,
			Path:             localAudio,
			FinalAudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}

	ok := runner.publishFinalAudio(context.Background(), "run-2", req, testRoutingContext(), NewExecutionContext("run-2", "corr-2"), result)
	require.False(t, ok, "publish with an empty canonical asset ID must fail closed")
	run, err := repo.Get(context.Background(), "run-2")
	require.NoError(t, err)
	require.Equal(t, RunStatusFailed, run.Status, "run must be failed on empty canonical asset ID")
}

func TestRunnerPublishFinalAudioFailsClosedWithoutDriveFileID(t *testing.T) {
	repo := newInMemRunRepository()
	runner := NewRunner(repo, newStubTextGenerator(defaultTestScenes()), newStubTranslator(), newStubVoiceoverGenerator(), newStubDocumentPublisher())
	runner.SetLogger(zap.NewNop())
	runner.SetFinalAudioPublisher(&stubFinalAudioPublisher{result: FinalAudioPublishResult{
		AssetID: "canonical-audio", DriveLink: "https://drive.google.com/file/d/abc",
	}})
	req := defaultTestRequest()
	localAudio := filepath.Join(t.TempDir(), "final-audio.m4a")
	require.NoError(t, os.WriteFile(localAudio, []byte("audio"), 0o600))
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: "run-no-drive-id", Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	result := &GenerateResult{FinalAudio: &FinalAudioReference{
		Path: localAudio, FinalAudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}

	if runner.publishFinalAudio(context.Background(), "run-no-drive-id", req, testRoutingContext(), NewExecutionContext("run-no-drive-id", "corr-3"), result) {
		t.Fatal("publisher result without DriveFileID must fail closed")
	}
}
